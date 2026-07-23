// engine.cpp - Becky Review 3 native video engine (mpv replacement).
// Architecture: SPEC-BECKY-VIDEO-ENGINE.md. Every hard lesson here was
// measured first in native/becky-engine-probe (see HANDOFF-VIDEO-ENGINE.md
// checkboxes + commit log on local/video-engine):
//  - decode runs on its OWN D3D11 device: a blocking Present on the render
//    device once starved a shared-device decoder to 15 fps.
//  - ID3D10Multithread protection ON both devices or the NVIDIA UMD segfaults.
//  - ring textures are legacy-SHARED (keyed mutex costs a ~78 ms GPU sync).
//  - seeks abort only when the target leaves their +/-15 window (exact-match
//    aborts livelocked playback).
//  - decoded AVFrames are held 4 deep before unref (async GPU reads from the
//    decoder pool).
// Audio is WASAPI shared/event-driven directly (no PortAudio: the app links
// with MSVC; the probe's mingw PortAudio static lib is not linkable here).
// ASCII only (house rule).

#include "engine.h"

#include <windows.h>
#include <d3d11.h>
#include <d3d10.h>
#include <dxgi.h>
#include <d3dcompiler.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <atomic>
#include <mutex>
#include <thread>
#include <vector>
#include <string>
#include <cstdio>

extern "C" {
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_d3d11va.h>
#include <libavutil/pixdesc.h>
#include <libswresample/swresample.h>
}

// main.cpp's crashLog is static (internal linkage); it exports this bridge.
void engineLog(const std::string& s);
static void crashLog(const std::string& s) { engineLog(s); }

namespace engine {

static double nowMs() {
    static LARGE_INTEGER freq = {};
    if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
    LARGE_INTEGER t; QueryPerformanceCounter(&t);
    return (double)t.QuadPart * 1000.0 / (double)freq.QuadPart;
}

// ------------------------------------------------------------ decode device

static ID3D11Device*        g_decDev = nullptr;
static ID3D11DeviceContext* g_decCtx = nullptr;
static AVBufferRef*         g_hwdev = nullptr;

static bool createDecodeDevice() {
    IDXGIFactory1* fac = nullptr;
    if (FAILED(CreateDXGIFactory1(__uuidof(IDXGIFactory1), (void**)&fac))) return false;
    bool ok = false;
    for (UINT i = 0; !ok; i++) {
        IDXGIAdapter1* ad = nullptr;
        if (fac->EnumAdapters1(i, &ad) == DXGI_ERROR_NOT_FOUND) break;
        D3D_FEATURE_LEVEL fl;
        HRESULT hr = D3D11CreateDevice(ad, D3D_DRIVER_TYPE_UNKNOWN, nullptr,
                                       D3D11_CREATE_DEVICE_BGRA_SUPPORT |
                                       D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                                       nullptr, 0, D3D11_SDK_VERSION,
                                       &g_decDev, &fl, &g_decCtx);
        ad->Release();
        if (FAILED(hr)) continue;
        ID3D11VideoDevice* vd = nullptr;
        if (SUCCEEDED(g_decDev->QueryInterface(__uuidof(ID3D11VideoDevice), (void**)&vd))) {
            vd->Release();
            ID3D10Multithread* mt = nullptr;
            if (SUCCEEDED(g_decCtx->QueryInterface(__uuidof(ID3D10Multithread), (void**)&mt))) {
                mt->SetMultithreadProtected(TRUE);
                mt->Release();
            }
            ok = true;
        } else {
            g_decCtx->Release(); g_decCtx = nullptr;
            g_decDev->Release(); g_decDev = nullptr;
        }
    }
    fac->Release();
    return ok;
}

static bool createHwDevice() {
    AVBufferRef* hw = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_D3D11VA);
    if (!hw) return false;
    AVHWDeviceContext* hctx = (AVHWDeviceContext*)hw->data;
    AVD3D11VADeviceContext* d3d = (AVD3D11VADeviceContext*)hctx->hwctx;
    g_decDev->AddRef();
    d3d->device = g_decDev;
    if (av_hwdevice_ctx_init(hw) < 0) { av_buffer_unref(&hw); return false; }
    g_hwdev = hw;
    return true;
}

// ------------------------------------------------------------ decoder

// get_format: pick D3D11 and build OUR frames ctx so the decoder's pool
// textures carry D3D11_BIND_SHADER_RESOURCE - the NV12->BGRA convert pass
// samples the pool slice directly (zero copies on the video path, spec s.2).
// Copy-out design (probe-proven): the decoder pool textures need NO special
// bind flags because ringStore copies the slice into a scratch NV12 texture
// first. Per-plane SRVs over the decoder's ARRAY texture were REJECTED by this
// driver (measured 2026-07-23: CreateShaderResourceView failed) - plain
// Texture2D SRVs over a copied non-array NV12 texture are what the probe
// proved working.
static enum AVPixelFormat pick_d3d11(AVCodecContext*, const enum AVPixelFormat* fmts) {
    for (const enum AVPixelFormat* p = fmts; *p != AV_PIX_FMT_NONE; p++)
        if (*p == AV_PIX_FMT_D3D11) return *p;
    crashLog("engine: d3d11 not offered for this stream, software fallback");
    return fmts[0];
}

struct Decoder {
    AVFormatContext* fmt = nullptr;
    AVCodecContext*  ctx = nullptr;
    int        vstream = -1;
    AVRational tb = {0, 1};
    AVRational fps = {30000, 1001};
    int64_t    startTs = 0;
    int        width = 0, height = 0;
    int64_t    nbFrames = 0;

    bool open(const char* path) {
        if (avformat_open_input(&fmt, path, nullptr, nullptr) < 0) return false;
        if (avformat_find_stream_info(fmt, nullptr) < 0) return false;
        vstream = av_find_best_stream(fmt, AVMEDIA_TYPE_VIDEO, -1, -1, nullptr, 0);
        if (vstream < 0) return false;
        AVStream* st = fmt->streams[vstream];
        tb = st->time_base;
        startTs = (st->start_time == AV_NOPTS_VALUE) ? 0 : st->start_time;
        if (st->avg_frame_rate.num > 0) fps = st->avg_frame_rate;
        const AVCodec* dec = avcodec_find_decoder(st->codecpar->codec_id);
        if (!dec) return false;
        ctx = avcodec_alloc_context3(dec);
        avcodec_parameters_to_context(ctx, st->codecpar);
        ctx->hw_device_ctx = av_buffer_ref(g_hwdev);
        ctx->get_format = pick_d3d11;
        ctx->extra_hw_frames = 10; // 4 delay-unref + ring-pass headroom
        if (avcodec_open2(ctx, dec, nullptr) < 0) return false;
        width = ctx->width; height = ctx->height;
        nbFrames = st->nb_frames > 0 ? st->nb_frames
                 : (int64_t)((double)fmt->duration / AV_TIME_BASE * av_q2d(fps));
        return true;
    }
    void close() {
        if (ctx) avcodec_free_context(&ctx);
        if (fmt) avformat_close_input(&fmt);
    }
};

static int nextFrame(Decoder& d, AVPacket* pkt, AVFrame* f) {
    for (;;) {
        int r = avcodec_receive_frame(d.ctx, f);
        if (r == 0) return 1;
        if (r == AVERROR_EOF) return 0;
        if (r != AVERROR(EAGAIN)) return r;
        for (;;) {
            r = av_read_frame(d.fmt, pkt);
            if (r == AVERROR_EOF) { avcodec_send_packet(d.ctx, nullptr); break; }
            if (r < 0) return r;
            if (pkt->stream_index != d.vstream) { av_packet_unref(pkt); continue; }
            r = avcodec_send_packet(d.ctx, pkt);
            av_packet_unref(pkt);
            if (r < 0 && r != AVERROR(EAGAIN)) return r;
            break;
        }
    }
}

static const int RING = 31;
static const int SEEK_ABORTED = -3;

// Frame-exact seek (spec s.4): BACKWARD to keyframe, decode forward to the
// frame containing target. Window-based abort (probe lesson).
static int seekExact(Decoder& d, AVPacket* pkt, AVFrame* f, int64_t targetTs,
                     const std::atomic<int64_t>* abortWant, int64_t targetFrame) {
    int64_t frameDur = av_rescale_q(1, av_inv_q(d.fps), d.tb);
    if (frameDur <= 0) frameDur = 1;
    int r = av_seek_frame(d.fmt, d.vstream, targetTs, AVSEEK_FLAG_BACKWARD);
    if (r < 0) return r;
    avcodec_flush_buffers(d.ctx);
    for (;;) {
        if (abortWant) {
            int64_t w = abortWant->load(std::memory_order_relaxed);
            if (w < targetFrame - RING / 2 || w > targetFrame + RING / 2)
                return SEEK_ABORTED;
        }
        r = nextFrame(d, pkt, f);
        if (r <= 0) return r;
        int64_t bet = f->best_effort_timestamp;
        int64_t dur = f->duration > 0 ? f->duration : frameDur;
        if (bet + dur > targetTs) return 1;
        av_frame_unref(f);
    }
}

// ------------------------------------------------------------ NV12->BGRA pass

static const char* kConvSrc =
"cbuffer cb : register(b0) { float2 uvScale; float2 pad; }\n"
"struct VSOut { float4 pos : SV_Position; float2 uv : TEXCOORD0; };\n"
"VSOut vsmain(uint id : SV_VertexID) {\n"
"  float2 p = float2((id << 1) & 2, id & 2);\n"
"  VSOut o; o.pos = float4(p * float2(2, -2) + float2(-1, 1), 0, 1);\n"
"  o.uv = p * uvScale; return o;\n"
"}\n"
"Texture2D texY : register(t0);\n"
"Texture2D texUV : register(t1);\n"
"SamplerState smp : register(s0);\n"
"float4 psmain(VSOut i) : SV_Target {\n"
"  float y = (texY.Sample(smp, i.uv).r - 16.0/255.0) * (255.0/219.0);\n"
"  float2 c = texUV.Sample(smp, i.uv).rg;\n"
"  float u = (c.r - 128.0/255.0) * (255.0/224.0);\n"
"  float v = (c.g - 128.0/255.0) * (255.0/224.0);\n"
"  float3 rgb = float3(y + 1.5748*v, y - 0.1873*u - 0.4681*v, y + 1.8556*u);\n"
"  return float4(saturate(rgb), 1.0);\n"
"}\n";

static ID3D11VertexShader* g_convVS = nullptr;
static ID3D11PixelShader*  g_convPS = nullptr;
static ID3D11SamplerState* g_convSamp = nullptr;
static ID3D11Buffer*       g_convCB0 = nullptr;
static ID3D11Buffer*       g_convCB1 = nullptr;

static bool convInit() {
    ID3DBlob *vsb = nullptr, *psb = nullptr, *err = nullptr;
    if (FAILED(D3DCompile(kConvSrc, strlen(kConvSrc), nullptr, nullptr, nullptr,
                          "vsmain", "vs_4_0", 0, 0, &vsb, &err))) {
        crashLog(std::string("engine: convert VS compile failed: ") +
                 (err ? (char*)err->GetBufferPointer() : "?"));
        return false;
    }
    if (FAILED(D3DCompile(kConvSrc, strlen(kConvSrc), nullptr, nullptr, nullptr,
                          "psmain", "ps_4_0", 0, 0, &psb, &err))) {
        crashLog(std::string("engine: convert PS compile failed: ") +
                 (err ? (char*)err->GetBufferPointer() : "?"));
        return false;
    }
    g_decDev->CreateVertexShader(vsb->GetBufferPointer(), vsb->GetBufferSize(), nullptr, &g_convVS);
    g_decDev->CreatePixelShader(psb->GetBufferPointer(), psb->GetBufferSize(), nullptr, &g_convPS);
    vsb->Release(); psb->Release();
    D3D11_SAMPLER_DESC sd = {};
    sd.Filter = D3D11_FILTER_MIN_MAG_MIP_LINEAR;
    sd.AddressU = sd.AddressV = sd.AddressW = D3D11_TEXTURE_ADDRESS_CLAMP;
    g_decDev->CreateSamplerState(&sd, &g_convSamp);
    D3D11_BUFFER_DESC bd = {};
    bd.ByteWidth = 16; bd.Usage = D3D11_USAGE_DEFAULT; bd.BindFlags = D3D11_BIND_CONSTANT_BUFFER;
    g_decDev->CreateBuffer(&bd, nullptr, &g_convCB0);
    g_decDev->CreateBuffer(&bd, nullptr, &g_convCB1);
    return g_convVS && g_convPS && g_convSamp && g_convCB0 && g_convCB1;
}

// ------------------------------------------------------------ ring

struct RingSlot {
    std::atomic<int64_t> frame{-1};
    ID3D11Texture2D* tex = nullptr;         // decode device, BGRA, SHARED
    ID3D11RenderTargetView* rtv = nullptr;
    HANDLE shared = nullptr;
    int w = 0, h = 0;
    uint32_t gen = 0;                       // bumped when tex is recreated
    // app-side cache (owned here, created on the app device)
    ID3D11Texture2D* appTex = nullptr;
    ID3D11ShaderResourceView* appSrv = nullptr;
    uint32_t appGen = 0;
};

static RingSlot g_ring[RING];
static std::atomic<int64_t> g_lastShown{-1};
static std::atomic<uint32_t> g_ringGen{1};

// Convert the decoded frame into slot's BGRA texture (decode thread only).
static bool ringStore(AVFrame* f, int64_t idx) {
    if (f->format != AV_PIX_FMT_D3D11) {
        static bool once = false;
        if (!once) { crashLog("engine: frame not d3d11 (sw fallback has no draw path yet)"); once = true; }
        return false;
    }
    ID3D11Texture2D* src = (ID3D11Texture2D*)f->data[0];
    UINT slice = (UINT)(intptr_t)f->data[1];
    RingSlot& slot = g_ring[idx % RING];
    slot.frame.store(-1, std::memory_order_release);
    int w = f->width, h = f->height;
    if (!slot.tex || slot.w != w || slot.h != h) {
        if (slot.rtv) { slot.rtv->Release(); slot.rtv = nullptr; }
        if (slot.tex) { slot.tex->Release(); slot.tex = nullptr; }
        D3D11_TEXTURE2D_DESC td = {};
        td.Width = w; td.Height = h; td.MipLevels = 1; td.ArraySize = 1;
        td.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        td.SampleDesc.Count = 1;
        td.Usage = D3D11_USAGE_DEFAULT;
        td.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
        td.MiscFlags = D3D11_RESOURCE_MISC_SHARED; // legacy shared, no keyed mutex (probe lesson)
        if (FAILED(g_decDev->CreateTexture2D(&td, nullptr, &slot.tex))) {
            static bool once = false;
            if (!once) { crashLog("engine: ring CreateTexture2D failed"); once = true; }
            return false;
        }
        g_decDev->CreateRenderTargetView(slot.tex, nullptr, &slot.rtv);
        IDXGIResource* res = nullptr;
        slot.shared = nullptr;
        if (SUCCEEDED(slot.tex->QueryInterface(__uuidof(IDXGIResource), (void**)&res))) {
            res->GetSharedHandle(&slot.shared);
            res->Release();
        }
        slot.w = w; slot.h = h;
        slot.gen = g_ringGen.fetch_add(1) + 1;
        if (!slot.rtv || !slot.shared) {
            static bool once = false;
            if (!once) { crashLog("engine: ring rtv/shared-handle creation failed"); once = true; }
            return false;
        }
    }
    D3D11_TEXTURE2D_DESC sd; src->GetDesc(&sd);
    // Copy the decoder slice into the scratch NV12 texture, then sample THAT.
    // (Per-plane SRVs directly on the decoder's array texture were rejected by
    // this driver - the probe-proven path is copy first, plain Texture2D SRVs.)
    static ID3D11Texture2D* scratch = nullptr;
    static ID3D11ShaderResourceView* scrY = nullptr;
    static ID3D11ShaderResourceView* scrUV = nullptr;
    static UINT scrW = 0, scrH = 0;
    if (!scratch || scrW != sd.Width || scrH != sd.Height) {
        if (scrY) { scrY->Release(); scrY = nullptr; }
        if (scrUV) { scrUV->Release(); scrUV = nullptr; }
        if (scratch) { scratch->Release(); scratch = nullptr; }
        D3D11_TEXTURE2D_DESC td2 = {};
        td2.Width = sd.Width; td2.Height = sd.Height;
        td2.MipLevels = 1; td2.ArraySize = 1;
        td2.Format = sd.Format; // NV12 / P010
        td2.SampleDesc.Count = 1;
        td2.Usage = D3D11_USAGE_DEFAULT;
        td2.BindFlags = D3D11_BIND_SHADER_RESOURCE;
        if (FAILED(g_decDev->CreateTexture2D(&td2, nullptr, &scratch))) {
            static bool once = false;
            if (!once) { crashLog("engine: scratch NV12 texture create failed"); once = true; }
            return false;
        }
        D3D11_SHADER_RESOURCE_VIEW_DESC sv = {};
        sv.ViewDimension = D3D11_SRV_DIMENSION_TEXTURE2D;
        sv.Texture2D.MipLevels = 1;
        sv.Format = DXGI_FORMAT_R8_UNORM;
        g_decDev->CreateShaderResourceView(scratch, &sv, &scrY);
        sv.Format = DXGI_FORMAT_R8G8_UNORM;
        g_decDev->CreateShaderResourceView(scratch, &sv, &scrUV);
        if (!scrY || !scrUV) {
            static bool once = false;
            if (!once) { crashLog("engine: scratch NV12 SRV create failed"); once = true; }
            return false;
        }
        scrW = sd.Width; scrH = sd.Height;
        crashLog("engine: scratch NV12 " + std::to_string(scrW) + "x" + std::to_string(scrH) + " ready");
    }
    g_decCtx->CopySubresourceRegion(scratch, 0, 0, 0, 0, src, slice, nullptr);
    ID3D11ShaderResourceView *srvY = scrY, *srvUV = scrUV;

    float cb0[4] = { (float)w / sd.Width, (float)h / sd.Height, 0, 0 };
    g_decCtx->UpdateSubresource(g_convCB0, 0, nullptr, cb0, 0, 0);
    D3D11_VIEWPORT vp = { 0, 0, (float)w, (float)h, 0, 1 };
    g_decCtx->RSSetViewports(1, &vp);
    g_decCtx->OMSetRenderTargets(1, &slot.rtv, nullptr);
    g_decCtx->IASetPrimitiveTopology(D3D11_PRIMITIVE_TOPOLOGY_TRIANGLELIST);
    g_decCtx->IASetInputLayout(nullptr);
    g_decCtx->VSSetShader(g_convVS, nullptr, 0);
    g_decCtx->VSSetConstantBuffers(0, 1, &g_convCB0);
    g_decCtx->PSSetShader(g_convPS, nullptr, 0);
    ID3D11ShaderResourceView* srvs[2] = { srvY, srvUV };
    g_decCtx->PSSetShaderResources(0, 2, srvs);
    g_decCtx->PSSetSamplers(0, 1, &g_convSamp);
    g_decCtx->Draw(3, 0);
    ID3D11ShaderResourceView* nul[2] = { nullptr, nullptr };
    g_decCtx->PSSetShaderResources(0, 2, nul);
    ID3D11RenderTargetView* nulRt = nullptr;
    g_decCtx->OMSetRenderTargets(1, &nulRt, nullptr);
    g_decCtx->Flush(); // submit before publishing (cross-device visibility)
    slot.frame.store(idx, std::memory_order_release);
    return true;
}

// ------------------------------------------------------------ program (modes)

struct Prog {
    std::vector<EngineSeg> segs;   // scrub mode = 1 whole-file segment
    std::vector<int64_t> prefix;   // output frame at each segment start
    int64_t totalFrames = 0;
    AVRational fps = {30000, 1001};
    bool reel = false;             // true: reel mode (audio + playing possible)
};

static std::mutex g_progMx;
static Prog g_prog;                       // guarded by g_progMx
static std::atomic<uint32_t> g_progGen{0};
static std::atomic<bool> g_quit{false};
static std::atomic<bool> g_playing{false};
static std::atomic<bool> g_ended{false};
static std::atomic<int64_t> g_scrubWant{0};
static double g_rate = 1.0;               // guarded by g_progMx
static std::atomic<bool>  g_videoUp{false};
static std::atomic<bool>  g_audioUp{false};
static HANDLE g_wake = nullptr;

// play clock anchors (guarded by g_progMx; read via snapshot in wantNow)
static int64_t g_anchorFrame = 0;
static int64_t g_audioBaseFrames = 0;
static double  g_anchorMs = 0;

static std::atomic<int64_t> g_audioFramesConsumed{0}; // WASAPI render thread

// current desired output frame
static int64_t wantNow() {
    std::lock_guard<std::mutex> lk(g_progMx);
    if (!g_prog.reel || !g_playing.load()) return g_scrubWant.load();
    double fpsv = av_q2d(g_prog.fps);
    int64_t f;
    if (g_rate == 1.0 && g_audioUp.load()) {
        int64_t cons = g_audioFramesConsumed.load() - g_audioBaseFrames;
        f = g_anchorFrame + (int64_t)((double)cons / 48000.0 * fpsv);
    } else {
        f = g_anchorFrame + (int64_t)((nowMs() - g_anchorMs) / 1000.0 * fpsv * g_rate);
    }
    if (f >= g_prog.totalFrames) { f = g_prog.totalFrames - 1; }
    if (f < 0) f = 0;
    return f;
}

// ------------------------------------------------------------ source pool

struct SrcPool {
    std::vector<std::pair<std::string, Decoder*>> open;
    Decoder* get(const std::string& p) {
        for (auto& e : open) if (e.first == p) return e.second;
        Decoder* d = new Decoder();
        if (!d->open(p.c_str())) {
            d->close(); delete d;
            crashLog("engine: cannot open source " + p);
            return nullptr;
        }
        open.push_back({p, d});
        return d;
    }
    void clear() { for (auto& e : open) { e.second->close(); delete e.second; } open.clear(); }
};

// ------------------------------------------------------------ video thread

static std::thread g_videoThread;

static void videoLoop() {
    AVPacket* pkt = av_packet_alloc();
    AVFrame*  f = av_frame_alloc();
    AVFrame* held[4] = {};
    int heldIdx = 0;
    auto holdFrame = [&](AVFrame* src) {
        if (!held[heldIdx]) held[heldIdx] = av_frame_alloc();
        av_frame_unref(held[heldIdx]);
        av_frame_move_ref(held[heldIdx], src);
        heldIdx = (heldIdx + 1) % 4;
    };
    SrcPool pool;
    uint32_t myGen = (uint32_t)-1;
    Prog prog;                 // local snapshot
    int64_t next = -1000000;
    int curSeg = -1;
    Decoder* dd = nullptr;
    int64_t lastSrcFrame = -1000000;
    std::atomic<int64_t> wantMirror{0}; // for seekExact's abort window

    auto segOf = [&](int64_t F) -> int {
        int lo = 0, hi = (int)prog.segs.size() - 1, best = 0;
        while (lo <= hi) {
            int mid = (lo + hi) / 2;
            if (prog.prefix[mid] <= F) { best = mid; lo = mid + 1; } else hi = mid - 1;
        }
        return best;
    };

    auto produce = [&](int64_t F) -> int {
        if (prog.segs.empty()) return 0;
        int si = segOf(F);
        const EngineSeg& sg = prog.segs[si];
        Decoder* d = pool.get(sg.source);
        if (!d) return 0;
        // Map through SECONDS then into the SOURCE's own rate, so a source
        // whose fps differs from the timeline still lands on the right frame.
        double srcSeconds = sg.in + (double)(F - prog.prefix[si]) * av_q2d(av_inv_q(prog.fps));
        int64_t srcFrame = llround(srcSeconds * av_q2d(d->fps));
        int r;
        if (si != curSeg || d != dd || srcFrame != lastSrcFrame + 1) {
            int64_t ts = d->startTs + av_rescale_q(srcFrame, av_inv_q(d->fps), d->tb);
            r = seekExact(*d, pkt, f, ts, &wantMirror, F);
            if (r != 1) return r;
        } else {
            r = nextFrame(*d, pkt, f);
            if (r != 1) return r;
        }
        curSeg = si; dd = d; lastSrcFrame = srcFrame;
        ringStore(f, F);
        holdFrame(f);
        return 1;
    };

    double lastWantChange = 0;
    int64_t prevWant = -1;
    while (!g_quit.load()) {
        uint32_t gen = g_progGen.load();
        if (gen != myGen) {
            std::lock_guard<std::mutex> lk(g_progMx);
            prog = g_prog;
            myGen = gen;
            next = -1000000; curSeg = -1; dd = nullptr; lastSrcFrame = -1000000;
        }
        if (prog.segs.empty()) { WaitForSingleObject(g_wake, 50); continue; }

        int64_t want = wantNow();
        wantMirror.store(want);
        if (want != prevWant) { prevWant = want; lastWantChange = nowMs(); }
        int64_t lo = want - RING / 2; if (lo < 0) lo = 0;
        int64_t hi = want + RING / 2;
        if (hi >= prog.totalFrames) hi = prog.totalFrames - 1;

        if ((next < lo && want - next >= 60) || next > hi + 1 || next < 0) {
            curSeg = -1; lastSrcFrame = -1000000; // force seek
            int r = produce(want);
            if (r == SEEK_ABORTED) { next = -1000000; continue; }
            if (r != 1) { WaitForSingleObject(g_wake, 20); continue; }
            next = want + 1;
            continue;
        }
        if (next > hi) {
            int64_t missing = -1;
            if (nowMs() - lastWantChange > 150.0) {
                for (int64_t i = lo; i < want; i++)
                    if (g_ring[i % RING].frame.load(std::memory_order_acquire) != i) { missing = i; break; }
            }
            if (missing >= 0) {
                curSeg = -1; lastSrcFrame = -1000000;
                int r = produce(missing);
                if (r == SEEK_ABORTED) { next = -1000000; continue; }
                if (r == 1) { next = missing + 1; continue; }
            }
            WaitForSingleObject(g_wake, 5);
            continue;
        }
        int r = produce(next);
        if (r == SEEK_ABORTED) { next = -1000000; continue; }
        if (r == 0) { WaitForSingleObject(g_wake, 20); next = -1000000; continue; }
        if (r < 0) {
            // corrupt packet / decode error: keep last frame, log once per burst
            static double lastErrLog = 0;
            if (nowMs() - lastErrLog > 5000) { crashLog("engine: decode error, holding last frame"); lastErrLog = nowMs(); }
            next = -1000000;
            WaitForSingleObject(g_wake, 10);
            continue;
        }
        next++;
    }
    for (int i = 0; i < 4; i++) if (held[i]) av_frame_free(&held[i]);
    av_packet_free(&pkt);
    av_frame_free(&f);
    pool.clear();
}

// ------------------------------------------------------------ audio (WASAPI direct)

struct AudioRing {
    static const int64_t CAP = 48000 * 2 * 4;
    std::vector<float> buf;
    std::atomic<int64_t> w{0}, r{0};
    AudioRing() : buf(CAP, 0.0f) {}
    int64_t writable() const { return CAP - (w.load() - r.load()); }
    void push(const float* src, int64_t n) {
        int64_t ww = w.load(std::memory_order_relaxed);
        for (int64_t i = 0; i < n; i++) buf[(ww + i) % CAP] = src[i];
        w.store(ww + n, std::memory_order_release);
    }
    int64_t pull(float* dst, int64_t n) {
        int64_t ww = w.load(std::memory_order_acquire), rr = r.load(std::memory_order_relaxed);
        int64_t avail = ww - rr; int64_t take = avail < n ? avail : n;
        if (take < 0) take = 0;
        for (int64_t i = 0; i < take; i++) dst[i] = buf[(rr + i) % CAP];
        for (int64_t i = take; i < n; i++) dst[i] = 0.0f;
        r.store(rr + take, std::memory_order_release);
        return take;
    }
    void flush() { r.store(w.load()); }
};

static AudioRing g_aring;
static std::thread g_wasapiThread;
static std::thread g_audioDecThread;
static std::atomic<bool> g_audioActive{false}; // reel playing at rate 1.0

// WASAPI shared event-driven render; the callback-side sample counter is the
// master clock (spec s.2). AUTOCONVERTPCM lets us always feed 48k float stereo.
static void wasapiLoop() {
    CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    IMMDeviceEnumerator* en = nullptr;
    IMMDevice* dev = nullptr;
    IAudioClient* ac = nullptr;
    IAudioRenderClient* rc = nullptr;
    HANDLE ev = nullptr;
    UINT32 bufFrames = 0;
    bool up = false;
    do {
        if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
                                    __uuidof(IMMDeviceEnumerator), (void**)&en))) break;
        if (FAILED(en->GetDefaultAudioEndpoint(eRender, eConsole, &dev))) break;
        if (FAILED(dev->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, (void**)&ac))) break;
        WAVEFORMATEXTENSIBLE fmt = {};
        fmt.Format.wFormatTag = WAVE_FORMAT_EXTENSIBLE;
        fmt.Format.nChannels = 2;
        fmt.Format.nSamplesPerSec = 48000;
        fmt.Format.wBitsPerSample = 32;
        fmt.Format.nBlockAlign = 8;
        fmt.Format.nAvgBytesPerSec = 48000 * 8;
        fmt.Format.cbSize = sizeof(WAVEFORMATEXTENSIBLE) - sizeof(WAVEFORMATEX);
        fmt.Samples.wValidBitsPerSample = 32;
        fmt.dwChannelMask = SPEAKER_FRONT_LEFT | SPEAKER_FRONT_RIGHT;
        fmt.SubFormat = KSDATAFORMAT_SUBTYPE_IEEE_FLOAT;
        REFERENCE_TIME dur = 20 * 10000; // 20 ms
        HRESULT hr = ac->Initialize(AUDCLNT_SHAREMODE_SHARED,
                                    AUDCLNT_STREAMFLAGS_EVENTCALLBACK |
                                    AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM |
                                    AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY,
                                    dur, 0, (WAVEFORMATEX*)&fmt, nullptr);
        if (FAILED(hr)) { crashLog("engine: WASAPI Initialize failed (silent playback, QPC clock)"); break; }
        ev = CreateEventW(nullptr, FALSE, FALSE, nullptr);
        if (FAILED(ac->SetEventHandle(ev))) break;
        if (FAILED(ac->GetBufferSize(&bufFrames))) break;
        if (FAILED(ac->GetService(__uuidof(IAudioRenderClient), (void**)&rc))) break;
        if (FAILED(ac->Start())) break;
        up = true;
    } while (0);
    g_audioUp.store(up);
    if (up) crashLog("engine: WASAPI up (48k float stereo shared, event-driven)");
    else crashLog("engine: WASAPI unavailable - silent playback, QPC clock (degrade 2)");
    if (up) {
        std::vector<float> tmp(bufFrames * 2);
        while (!g_quit.load()) {
            if (WaitForSingleObject(ev, 200) != WAIT_OBJECT_0) continue;
            UINT32 pad = 0;
            if (FAILED(ac->GetCurrentPadding(&pad))) break;
            UINT32 want = bufFrames - pad;
            if (!want) continue;
            BYTE* out = nullptr;
            if (FAILED(rc->GetBuffer(want, &out))) break;
            if (g_audioActive.load()) {
                int64_t got = g_aring.pull((float*)out, (int64_t)want * 2);
                g_audioFramesConsumed.fetch_add(want);
                static bool once = false;
                if (!once && got > 0) {
                    crashLog("engine: audio FLOWING (device consuming real samples)");
                    once = true;
                }
            } else {
                memset(out, 0, (size_t)want * 8);
            }
            rc->ReleaseBuffer(want, 0);
        }
        ac->Stop();
    }
    if (rc) rc->Release();
    if (ac) ac->Release();
    if (dev) dev->Release();
    if (en) en->Release();
    if (ev) CloseHandle(ev);
    CoUninitialize();
}

// Audio segment-chain decode: sample-exact butt splices (probe-proven).
static void audioDecLoop() {
    const int RATE = 48000, CH = 2;
    uint32_t myGen = (uint32_t)-1;
    Prog prog;
    double startSec = 0;
    while (!g_quit.load()) {
        uint32_t gen = g_progGen.load();
        if (gen != myGen) {
            std::lock_guard<std::mutex> lk(g_progMx);
            prog = g_prog;
            myGen = gen;
            startSec = (double)g_anchorFrame * av_q2d(av_inv_q(prog.fps));
            g_aring.flush();
        }
        if (!prog.reel || !g_playing.load() || !g_audioActive.load()) { Sleep(10); continue; }

        // find starting segment + offset for startSec
        double acc = 0; size_t si = 0; double segOff = 0;
        for (; si < prog.segs.size(); si++) {
            double len = prog.segs[si].out - prog.segs[si].in;
            if (acc + len > startSec) { segOff = startSec - acc; break; }
            acc += len;
        }
        for (; si < prog.segs.size(); si++) {
            if (g_quit.load() || g_progGen.load() != myGen || !g_playing.load()) break;
            const EngineSeg& sg = prog.segs[si];
            double segIn = sg.in + segOff;
            segOff = 0;
            int64_t needSamples = llround((sg.out - segIn) * RATE);
            if (needSamples <= 0) continue;
            AVFormatContext* fmt = nullptr;
            AVCodecContext* actx = nullptr;
            SwrContext* swr = nullptr;
            int astream = -1;
            bool okA = false;
            if (avformat_open_input(&fmt, sg.source.c_str(), nullptr, nullptr) == 0 &&
                avformat_find_stream_info(fmt, nullptr) >= 0) {
                astream = av_find_best_stream(fmt, AVMEDIA_TYPE_AUDIO, -1, -1, nullptr, 0);
                if (astream >= 0) {
                    AVStream* st = fmt->streams[astream];
                    const AVCodec* dec = avcodec_find_decoder(st->codecpar->codec_id);
                    if (dec) {
                        actx = avcodec_alloc_context3(dec);
                        avcodec_parameters_to_context(actx, st->codecpar);
                        if (avcodec_open2(actx, dec, nullptr) == 0) {
                            AVChannelLayout outL = AV_CHANNEL_LAYOUT_STEREO;
                            if (swr_alloc_set_opts2(&swr, &outL, AV_SAMPLE_FMT_FLT, RATE,
                                                    &actx->ch_layout, actx->sample_fmt,
                                                    actx->sample_rate, 0, nullptr) >= 0 &&
                                swr_init(swr) >= 0)
                                okA = true;
                        }
                    }
                }
            }
            int64_t pushed = 0;
            if (okA) {
                av_seek_frame(fmt, -1, (int64_t)(segIn * AV_TIME_BASE), AVSEEK_FLAG_BACKWARD);
                AVPacket* pkt = av_packet_alloc();
                AVFrame* f = av_frame_alloc();
                std::vector<float> tmp;
                AVRational atb = fmt->streams[astream]->time_base;
                bool leadDone = false;
                while (pushed < needSamples && !g_quit.load() &&
                       g_progGen.load() == myGen && g_playing.load()) {
                    int r = avcodec_receive_frame(actx, f);
                    if (r == AVERROR(EAGAIN)) {
                        int rr = av_read_frame(fmt, pkt);
                        if (rr < 0) break;
                        if (pkt->stream_index != astream) { av_packet_unref(pkt); continue; }
                        avcodec_send_packet(actx, pkt);
                        av_packet_unref(pkt);
                        continue;
                    }
                    if (r < 0) break;
                    double st = (f->best_effort_timestamp == AV_NOPTS_VALUE)
                              ? -1.0 : f->best_effort_timestamp * av_q2d(atb);
                    int64_t outSamples = swr_get_out_samples(swr, f->nb_samples);
                    if (outSamples <= 0) { av_frame_unref(f); continue; }
                    if ((int64_t)tmp.size() < outSamples * CH) tmp.resize(outSamples * CH);
                    uint8_t* outPtrs[1] = { (uint8_t*)tmp.data() };
                    int got = swr_convert(swr, outPtrs, (int)outSamples,
                                          (const uint8_t**)f->data, f->nb_samples);
                    av_frame_unref(f);
                    if (got <= 0) continue;
                    int64_t skip = 0;
                    if (!leadDone) {
                        if (st >= 0 && st < segIn) skip = llround((segIn - st) * RATE);
                        if (skip >= got) continue;
                        leadDone = true;
                    }
                    int64_t take = got - skip;
                    if (take > needSamples - pushed) take = needSamples - pushed;
                    const float* sp = tmp.data() + skip * CH;
                    int64_t done = 0;
                    while (done < take && !g_quit.load() && g_progGen.load() == myGen) {
                        int64_t chunk = take - done; if (chunk > 2048) chunk = 2048;
                        while (g_aring.writable() < chunk * CH && !g_quit.load() &&
                               g_progGen.load() == myGen) Sleep(2);
                        if (g_progGen.load() != myGen) break;
                        g_aring.push(sp + done * CH, chunk * CH);
                        done += chunk;
                    }
                    pushed += done;
                }
                av_packet_free(&pkt);
                av_frame_free(&f);
            }
            // silence-pad the remainder so the NEXT segment stays sample-exact
            if (g_progGen.load() == myGen && g_playing.load()) {
                std::vector<float> zeros(2048 * CH, 0.0f);
                while (pushed < needSamples && !g_quit.load() && g_progGen.load() == myGen) {
                    int64_t chunk = needSamples - pushed; if (chunk > 2048) chunk = 2048;
                    while (g_aring.writable() < chunk * CH && !g_quit.load() &&
                           g_progGen.load() == myGen) Sleep(2);
                    if (g_progGen.load() != myGen) break;
                    g_aring.push(zeros.data(), chunk * CH);
                    pushed += chunk;
                }
            }
            if (swr) swr_free(&swr);
            if (actx) avcodec_free_context(&actx);
            if (fmt) avformat_close_input(&fmt);
        }
        // chain finished (or interrupted): wait for a new generation/play state
        while (!g_quit.load() && g_progGen.load() == myGen && g_playing.load()) {
            // playback end detection: all samples consumed -> ended
            std::lock_guard<std::mutex> lk(g_progMx);
            double fpsv = av_q2d(g_prog.fps);
            int64_t f = g_anchorFrame +
                (int64_t)((double)(g_audioFramesConsumed.load() - g_audioBaseFrames) / 48000.0 * fpsv);
            if (g_prog.reel && f >= g_prog.totalFrames - 1) {
                g_ended.store(true);
            }
            Sleep(20);
        }
    }
}

// ------------------------------------------------------------ public API

bool init() {
    if (g_videoUp.load()) return true;
    if (!createDecodeDevice()) { crashLog("engine: no video-capable D3D11 adapter"); return false; }
    if (!createHwDevice())     { crashLog("engine: d3d11va hw device init failed"); return false; }
    if (!convInit())           return false;
    g_wake = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    g_quit.store(false);
    g_videoThread = std::thread(videoLoop);
    g_wasapiThread = std::thread(wasapiLoop);
    g_audioDecThread = std::thread(audioDecLoop);
    g_videoUp.store(true);
    crashLog("engine: up (own decode device, D3D11VA, WASAPI)");
    return true;
}

void shutdown() {
    if (!g_videoUp.load()) return;
    g_quit.store(true);
    SetEvent(g_wake);
    if (g_videoThread.joinable()) g_videoThread.join();
    if (g_wasapiThread.joinable()) g_wasapiThread.join();
    if (g_audioDecThread.joinable()) g_audioDecThread.join();
    for (auto& s : g_ring) {
        if (s.appSrv) { s.appSrv->Release(); s.appSrv = nullptr; }
        if (s.appTex) { s.appTex->Release(); s.appTex = nullptr; }
        if (s.rtv) { s.rtv->Release(); s.rtv = nullptr; }
        if (s.tex) { s.tex->Release(); s.tex = nullptr; }
        s.frame.store(-1);
    }
    if (g_convVS) g_convVS->Release();
    if (g_convPS) g_convPS->Release();
    if (g_convSamp) g_convSamp->Release();
    if (g_convCB0) g_convCB0->Release();
    if (g_convCB1) g_convCB1->Release();
    if (g_hwdev) av_buffer_unref(&g_hwdev);
    if (g_decCtx) g_decCtx->Release();
    if (g_decDev) g_decDev->Release();
    g_videoUp.store(false);
}

bool available() { return g_videoUp.load(); }
bool audioUp()   { return g_audioUp.load(); }

void showSource(const std::string& source, double srcSec) {
    if (!g_videoUp.load()) return;
    bool needProg;
    {
        std::lock_guard<std::mutex> lk(g_progMx);
        needProg = g_prog.reel || g_prog.segs.size() != 1 ||
                   g_prog.segs[0].source != source;
        if (needProg) {
            g_prog.segs.assign(1, EngineSeg{source, 0.0, 24 * 3600.0});
            g_prog.prefix.assign(1, 0);
            g_prog.totalFrames = INT64_MAX / 4; // real bound enforced by decode EOF
            g_prog.fps = {30000, 1001};         // refined by the decoder's own fps at map time
            g_prog.reel = false;
        }
        g_playing.store(false);
        g_audioActive.store(false);
        g_ended.store(false);
        g_scrubWant.store(llround(srcSec * av_q2d(g_prog.fps)));
    }
    if (needProg) g_progGen.fetch_add(1);
    SetEvent(g_wake);
}

void enterReel(const std::vector<EngineSeg>& segs, double fps, double startSec, double rate) {
    if (!g_videoUp.load() || segs.empty()) return;
    {
        std::lock_guard<std::mutex> lk(g_progMx);
        g_prog.segs = segs;
        g_prog.prefix.clear();
        int64_t acc = 0;
        AVRational fr = (fps > 29.9 && fps < 30.0) ? AVRational{30000, 1001}
                       : av_d2q(fps, 100000);
        g_prog.fps = fr;
        for (auto& s : segs) {
            g_prog.prefix.push_back(acc);
            int64_t n = llround((s.out - s.in) * fps);
            acc += (n > 0 ? n : 1);
        }
        g_prog.totalFrames = acc;
        g_prog.reel = true;
        g_rate = rate;
        g_anchorFrame = llround(startSec * fps);
        if (g_anchorFrame < 0) g_anchorFrame = 0;
        if (g_anchorFrame >= acc) g_anchorFrame = acc - 1;
        g_audioBaseFrames = g_audioFramesConsumed.load();
        g_anchorMs = nowMs();
        g_scrubWant.store(g_anchorFrame);
        g_ended.store(false);
        g_playing.store(true);
        g_audioActive.store(rate == 1.0 && g_audioUp.load());
    }
    g_progGen.fetch_add(1);
    SetEvent(g_wake);
}

void exitReel() {
    {
        std::lock_guard<std::mutex> lk(g_progMx);
        if (!g_prog.reel) return;
        // freeze the playhead where it is
        double fpsv = av_q2d(g_prog.fps);
        int64_t f;
        if (g_rate == 1.0 && g_audioUp.load())
            f = g_anchorFrame + (int64_t)((double)(g_audioFramesConsumed.load() - g_audioBaseFrames) / 48000.0 * fpsv);
        else
            f = g_anchorFrame + (int64_t)((nowMs() - g_anchorMs) / 1000.0 * fpsv * g_rate);
        if (f >= g_prog.totalFrames) f = g_prog.totalFrames - 1;
        if (f < 0) f = 0;
        g_scrubWant.store(f);
        g_playing.store(false);
        g_audioActive.store(false);
    }
    g_progGen.fetch_add(1); // audio chain stops, ring flushes
    SetEvent(g_wake);
}

void seekReel(double compSec) {
    {
        std::lock_guard<std::mutex> lk(g_progMx);
        if (!g_prog.reel) return;
        double fps = av_q2d(g_prog.fps);
        g_anchorFrame = llround(compSec * fps);
        if (g_anchorFrame < 0) g_anchorFrame = 0;
        if (g_anchorFrame >= g_prog.totalFrames) g_anchorFrame = g_prog.totalFrames - 1;
        g_audioBaseFrames = g_audioFramesConsumed.load();
        g_anchorMs = nowMs();
        g_scrubWant.store(g_anchorFrame);
        g_ended.store(false);
    }
    g_progGen.fetch_add(1); // audio re-chains from the new spot
    SetEvent(g_wake);
}

void setRate(double rate) {
    std::lock_guard<std::mutex> lk(g_progMx);
    if (!g_prog.reel) { g_rate = rate; return; }
    // re-anchor at the current position under the old rate first
    double fpsv = av_q2d(g_prog.fps);
    int64_t f;
    if (g_rate == 1.0 && g_audioUp.load())
        f = g_anchorFrame + (int64_t)((double)(g_audioFramesConsumed.load() - g_audioBaseFrames) / 48000.0 * fpsv);
    else
        f = g_anchorFrame + (int64_t)((nowMs() - g_anchorMs) / 1000.0 * fpsv * g_rate);
    g_anchorFrame = f;
    g_audioBaseFrames = g_audioFramesConsumed.load();
    g_anchorMs = nowMs();
    g_rate = rate;
    // ponytail: rate != 1.0 plays SILENT on the QPC clock (no time-stretch
    // tonight); upgrade path is a resampler stage in audioDecLoop.
    g_audioActive.store(rate == 1.0 && g_audioUp.load() && g_playing.load());
}

bool reelActive() {
    std::lock_guard<std::mutex> lk(g_progMx);
    return g_prog.reel && g_playing.load();
}
bool reelEnded() { return g_ended.load(); }

double clockSec() {
    std::lock_guard<std::mutex> lk(g_progMx);
    if (!g_prog.reel || !g_playing.load()) return -1.0;
    double fpsv = av_q2d(g_prog.fps);
    double f;
    if (g_rate == 1.0 && g_audioUp.load())
        f = (double)g_anchorFrame + (double)(g_audioFramesConsumed.load() - g_audioBaseFrames) / 48000.0 * fpsv;
    else
        f = (double)g_anchorFrame + (nowMs() - g_anchorMs) / 1000.0 * fpsv * g_rate;
    double maxF = (double)(g_prog.totalFrames - 1);
    if (f > maxF) f = maxF;
    return f / fpsv;
}

ID3D11ShaderResourceView* currentFrameSRV(ID3D11Device* appDev, int* w, int* h) {
    if (!g_videoUp.load() || !appDev) return nullptr;
    auto appSide = [&](RingSlot& s) -> ID3D11ShaderResourceView* {
        if (s.appGen != s.gen || !s.appSrv) {
            if (s.appSrv) { s.appSrv->Release(); s.appSrv = nullptr; }
            if (s.appTex) { s.appTex->Release(); s.appTex = nullptr; }
            if (!s.shared) return nullptr;
            if (FAILED(appDev->OpenSharedResource(s.shared, __uuidof(ID3D11Texture2D), (void**)&s.appTex))) {
                static bool once = false;
                if (!once) { crashLog("engine: app-side OpenSharedResource failed"); once = true; }
                return nullptr;
            }
            if (FAILED(appDev->CreateShaderResourceView(s.appTex, nullptr, &s.appSrv))) {
                s.appTex->Release(); s.appTex = nullptr;
                return nullptr;
            }
            s.appGen = s.gen;
        }
        if (w) *w = s.w;
        if (h) *h = s.h;
        return s.appSrv;
    };
    int64_t want = wantNow();
    RingSlot& slot = g_ring[want % RING];
    if (slot.frame.load(std::memory_order_acquire) == want) {
        ID3D11ShaderResourceView* v = appSide(slot);
        if (v) { g_lastShown.store(want); return v; }
    }
    int64_t ls = g_lastShown.load();
    if (ls >= 0) {
        RingSlot& p = g_ring[ls % RING];
        if (p.frame.load(std::memory_order_acquire) == ls) {
            ID3D11ShaderResourceView* v = appSide(p);
            if (v) return v;
        }
    }
    return nullptr;
}

} // namespace engine
