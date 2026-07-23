// becky-engine-probe - standalone harness for the Becky Review 3 native video
// engine (HANDOFF-VIDEO-ENGINE.md steps 2-5, architecture in
// SPEC-BECKY-VIDEO-ENGINE.md). Single file by design: these pieces get lifted
// into native/becky-review at step 6.
//
// Modes:
//   --decode <file> --frames N            step 2: demux + D3D11VA hw decode proof
//   --seek-test <file> --fps A/B --targets a,b,c   step 3: frame-exact seek proof
//   --play <file>                         step 4: window + ring + frame stepping
//   --play-reel <reel.json> --report-sync step 5: segments + WASAPI audio clock
//
// Build (MSYS2 mingw64): see build.sh next to this file.
// ASCII only in this file (house rule).

#include <windows.h>
#include <d3d11.h>
#include <dxgi.h>
#include <d3dcompiler.h>
#include <cstdio>
#include <cstdint>
#include <cstring>
#include <cstdlib>
#include <string>
#include <vector>
#include <atomic>
#include <thread>
#include <chrono>

extern "C" {
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_d3d11va.h>
#include <libavutil/pixdesc.h>
#include <libavutil/opt.h>
#include <libswresample/swresample.h>
}

static double nowMs() {
    static LARGE_INTEGER freq = { };
    if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
    LARGE_INTEGER t; QueryPerformanceCounter(&t);
    return (double)t.QuadPart * 1000.0 / (double)freq.QuadPart;
}

// ---------------------------------------------------------------- D3D11 device

static ID3D11Device*        g_dev = nullptr;
static ID3D11DeviceContext* g_devctx = nullptr;

// On this machine the DEFAULT adapter is "Microsoft Basic Render Driver"
// (software, no ID3D11VideoDevice) and the RTX 3070 is adapter 1 - so never
// create with a NULL adapter. Enumerate and take the first adapter whose
// device exposes ID3D11VideoDevice (measured 2026-07-22, diag_adapters.cpp).
static bool createD3D11Device() {
    IDXGIFactory1* fac = nullptr;
    if (FAILED(CreateDXGIFactory1(__uuidof(IDXGIFactory1), (void**)&fac))) {
        fprintf(stderr, "CreateDXGIFactory1 failed\n");
        return false;
    }
    for (UINT i = 0; ; i++) {
        IDXGIAdapter1* ad = nullptr;
        if (fac->EnumAdapters1(i, &ad) == DXGI_ERROR_NOT_FOUND) break;
        DXGI_ADAPTER_DESC1 desc; ad->GetDesc1(&desc);
        ID3D11Device* dev = nullptr; ID3D11DeviceContext* ctx = nullptr;
        D3D_FEATURE_LEVEL fl;
        HRESULT hr = D3D11CreateDevice(ad, D3D_DRIVER_TYPE_UNKNOWN, nullptr,
                                       D3D11_CREATE_DEVICE_BGRA_SUPPORT |
                                       D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                                       nullptr, 0, D3D11_SDK_VERSION,
                                       &dev, &fl, &ctx);
        ad->Release();
        if (FAILED(hr)) continue;
        ID3D11VideoDevice* vd = nullptr;
        if (SUCCEEDED(dev->QueryInterface(__uuidof(ID3D11VideoDevice), (void**)&vd))) {
            vd->Release();
            g_dev = dev; g_devctx = ctx;
            fprintf(stderr, "d3d11 device on adapter %u: %ls\n", i, desc.Description);
            fac->Release();
            return true;
        }
        ctx->Release(); dev->Release();
    }
    fac->Release();
    fprintf(stderr, "no video-capable D3D11 adapter found\n");
    return false;
}

// Create the FFmpeg hw device context FROM our existing D3D11 device
// (spec section 4: av_hwdevice_ctx_alloc + supply device + _init, NOT _create).
static AVBufferRef* createHwDeviceFromD3D() {
    AVBufferRef* hw = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_D3D11VA);
    if (!hw) { fprintf(stderr, "av_hwdevice_ctx_alloc failed\n"); return nullptr; }
    AVHWDeviceContext* hctx = (AVHWDeviceContext*)hw->data;
    AVD3D11VADeviceContext* d3d = (AVD3D11VADeviceContext*)hctx->hwctx;
    g_dev->AddRef();
    d3d->device = g_dev;
    int r = av_hwdevice_ctx_init(hw);
    if (r < 0) {
        char buf[128]; av_strerror(r, buf, sizeof buf);
        fprintf(stderr, "av_hwdevice_ctx_init failed: %s\n", buf);
        av_buffer_unref(&hw);
        return nullptr;
    }
    return hw;
}

// ---------------------------------------------------------------- decoder

static bool g_d3d11Offered = false;

static enum AVPixelFormat pick_d3d11(AVCodecContext*, const enum AVPixelFormat* fmts) {
    for (const enum AVPixelFormat* p = fmts; *p != AV_PIX_FMT_NONE; p++) {
        if (*p == AV_PIX_FMT_D3D11) { g_d3d11Offered = true; return *p; }
    }
    fprintf(stderr, "get_format: AV_PIX_FMT_D3D11 not offered; offers were:");
    for (const enum AVPixelFormat* p = fmts; *p != AV_PIX_FMT_NONE; p++)
        fprintf(stderr, " %s", av_get_pix_fmt_name(*p));
    fprintf(stderr, "\n");
    return fmts[0]; // software fallback, same code path (spec section 2)
}

struct Decoder {
    AVFormatContext* fmt = nullptr;
    AVCodecContext*  ctx = nullptr;
    int        vstream = -1;
    AVRational tb = {0, 1};
    int64_t    startTs = 0;   // stream start_time in tb units (0 if none)
    int        width = 0, height = 0;

    bool open(const char* path, AVBufferRef* hwdev) {
        int r = avformat_open_input(&fmt, path, nullptr, nullptr);
        if (r < 0) { err("avformat_open_input", r); return false; }
        r = avformat_find_stream_info(fmt, nullptr);
        if (r < 0) { err("avformat_find_stream_info", r); return false; }
        vstream = av_find_best_stream(fmt, AVMEDIA_TYPE_VIDEO, -1, -1, nullptr, 0);
        if (vstream < 0) { fprintf(stderr, "no video stream in %s\n", path); return false; }
        AVStream* st = fmt->streams[vstream];
        tb = st->time_base;
        startTs = (st->start_time == AV_NOPTS_VALUE) ? 0 : st->start_time;
        const AVCodec* dec = avcodec_find_decoder(st->codecpar->codec_id);
        if (!dec) { fprintf(stderr, "no decoder for codec\n"); return false; }
        ctx = avcodec_alloc_context3(dec);
        avcodec_parameters_to_context(ctx, st->codecpar);
        if (hwdev) {
            ctx->hw_device_ctx = av_buffer_ref(hwdev);
            ctx->get_format = pick_d3d11;
        }
        r = avcodec_open2(ctx, dec, nullptr);
        if (r < 0) { err("avcodec_open2", r); return false; }
        width = ctx->width; height = ctx->height;
        return true;
    }

    void close() {
        if (ctx) avcodec_free_context(&ctx);
        if (fmt) avformat_close_input(&fmt);
    }

    static void err(const char* what, int r) {
        char buf[128]; av_strerror(r, buf, sizeof buf);
        fprintf(stderr, "%s: %s\n", what, buf);
    }
};

// Pull the next decoded video frame into f. Returns 1 ok, 0 eof, <0 error.
static int nextFrame(Decoder& d, AVPacket* pkt, AVFrame* f) {
    for (;;) {
        int r = avcodec_receive_frame(d.ctx, f);
        if (r == 0) return 1;
        if (r == AVERROR_EOF) return 0;
        if (r != AVERROR(EAGAIN)) return r;
        // need more input
        for (;;) {
            r = av_read_frame(d.fmt, pkt);
            if (r == AVERROR_EOF) {
                avcodec_send_packet(d.ctx, nullptr); // flush
                break;
            }
            if (r < 0) return r;
            if (pkt->stream_index != d.vstream) { av_packet_unref(pkt); continue; }
            r = avcodec_send_packet(d.ctx, pkt);
            av_packet_unref(pkt);
            if (r < 0 && r != AVERROR(EAGAIN)) return r;
            break;
        }
    }
}

// ---------------------------------------------------------------- step 2: --decode

static int cmdDecode(const char* path, int nframes) {
    if (!createD3D11Device()) return 2;
    AVBufferRef* hwdev = createHwDeviceFromD3D();
    if (!hwdev) fprintf(stderr, "hw device init failed - software fallback\n");

    Decoder d;
    if (!d.open(path, hwdev)) return 2;
    printf("opened %s: %dx%d codec=%s tb=%d/%d start_ts=%lld\n",
           path, d.width, d.height, d.ctx->codec->name,
           d.tb.num, d.tb.den, (long long)d.startTs);

    AVPacket* pkt = av_packet_alloc();
    AVFrame*  f = av_frame_alloc();
    int decoded = 0, hwc = 0, swc = 0;
    double t0 = nowMs();
    while (decoded < nframes) {
        int r = nextFrame(d, pkt, f);
        if (r == 0) break;
        if (r < 0) { Decoder::err("decode", r); break; }
        const char* fname = av_get_pix_fmt_name((AVPixelFormat)f->format);
        double pts = (double)(f->best_effort_timestamp - d.startTs) * av_q2d(d.tb);
        printf("{idx:%d, pts:%.5f, format:%s}\n", decoded, pts, fname ? fname : "?");
        if (f->format == AV_PIX_FMT_D3D11) hwc++; else swc++;
        decoded++;
        av_frame_unref(f);
    }
    double elapsed = nowMs() - t0;
    printf("decoded=%d hw=%d sw=%d avg_decode_ms=%.2f\n",
           decoded, hwc, swc, decoded ? elapsed / decoded : 0.0);

    if (hwc == 0) {
        fprintf(stderr, "hw=0 diagnostics: av_hwdevice_iterate_types:");
        enum AVHWDeviceType t = AV_HWDEVICE_TYPE_NONE;
        while ((t = av_hwdevice_iterate_types(t)) != AV_HWDEVICE_TYPE_NONE)
            fprintf(stderr, " %s", av_hwdevice_get_type_name(t));
        fprintf(stderr, "\nd3d11 offered by get_format: %s\n", g_d3d11Offered ? "yes" : "no");
    }
    av_packet_free(&pkt);
    av_frame_free(&f);
    d.close();
    if (hwdev) av_buffer_unref(&hwdev);
    return (decoded > 0) ? 0 : 2;
}

// ---------------------------------------------------------------- step 3: --seek-test

// Frame-exact seek per spec section 4: AVSEEK_FLAG_BACKWARD to the keyframe at
// or before target, flush, then decode forward discarding until the frame whose
// interval contains target_ts. Returns 1 and leaves the frame in f on success.
static int seekExact(Decoder& d, AVPacket* pkt, AVFrame* f,
                     int64_t targetTs, AVRational fps, int* framesScanned) {
    int64_t frameDurTb = av_rescale_q(1, av_inv_q(fps), d.tb);
    if (frameDurTb <= 0) frameDurTb = 1;
    int r = av_seek_frame(d.fmt, d.vstream, targetTs, AVSEEK_FLAG_BACKWARD);
    if (r < 0) { Decoder::err("av_seek_frame", r); return r; }
    avcodec_flush_buffers(d.ctx);
    int scanned = 0;
    for (;;) {
        r = nextFrame(d, pkt, f);
        if (r <= 0) return r; // eof before target or error
        scanned++;
        int64_t bet = f->best_effort_timestamp;
        int64_t dur = f->duration > 0 ? f->duration : frameDurTb;
        if (bet + dur > targetTs) break; // this frame covers the target
        av_frame_unref(f);
    }
    if (framesScanned) *framesScanned = scanned;
    return 1;
}

static int cmdSeekTest(const char* path, AVRational fps, const std::vector<int64_t>& targets) {
    if (!createD3D11Device()) return 2;
    AVBufferRef* hwdev = createHwDeviceFromD3D();
    Decoder d;
    if (!d.open(path, hwdev)) return 2;

    AVPacket* pkt = av_packet_alloc();
    AVFrame*  f = av_frame_alloc();
    int failures = 0;
    for (int64_t target : targets) {
        // pts of frame N at fps num/den is N*den/num seconds
        int64_t targetTs = d.startTs + av_rescale_q(target, av_inv_q(fps), d.tb);
        int scanned = 0;
        double t0 = nowMs();
        int r = seekExact(d, pkt, f, targetTs, fps, &scanned);
        double ms = nowMs() - t0;
        if (r != 1) {
            printf("target=%lld FAILED (r=%d)\n", (long long)target, r);
            failures++;
            continue;
        }
        double ptsSec = (double)(f->best_effort_timestamp - d.startTs) * av_q2d(d.tb);
        // frame index at the true rate, by rounding (spec: compare by index)
        long long landed = llround(ptsSec * av_q2d(fps));
        printf("target=%lld landed=%lld pts=%.5f (decoded_forward=%d frames, seek_ms=%.1f, format=%s)\n",
               (long long)target, landed, ptsSec, scanned, ms,
               av_get_pix_fmt_name((AVPixelFormat)f->format));
        if (landed != target) failures++;
        av_frame_unref(f);
    }
    printf("seek-test: %zu targets, %d failures\n", targets.size(), failures);
    av_packet_free(&pkt);
    av_frame_free(&f);
    d.close();
    if (hwdev) av_buffer_unref(&hwdev);
    return failures ? 1 : 0;
}

// ---------------------------------------------------------------- step 4: --play
//
// Bare Win32 + D3D11 window. Decode thread fills a +/-15 frame ring of
// ring-owned NV12 textures (slices COPIED out immediately, spec section 4
// risk 2); UI thread draws the due frame as an NV12->RGB quad and presents.
// Arrow keys step +/-1 frame, PgUp/PgDn +/-30 (1 s), Space play/pause, Esc quit.

static const int RING = 31; // +/-15 around the playhead

struct RingSlot {
    std::atomic<int64_t> frame{-1};
    ID3D11Texture2D* tex = nullptr;
    ID3D11ShaderResourceView* srvY = nullptr;
    ID3D11ShaderResourceView* srvUV = nullptr;
};

struct PlayShared {
    RingSlot ring[RING];
    std::atomic<int64_t> want{0};      // playhead frame the UI wants shown
    std::atomic<bool>    quit{false};
    std::atomic<int64_t> eofAt{INT64_MAX}; // first frame index past EOF, if hit
    HANDLE wake = nullptr;             // decode thread wake-up
    AVD3D11VADeviceContext* d3dlock = nullptr; // FFmpeg's device lock: serialize ctx use
    int codedW = 0, codedH = 0;        // decoder texture dims (may exceed display)
};

static void d3dLock(PlayShared& s)   { if (s.d3dlock) s.d3dlock->lock(s.d3dlock->lock_ctx); }
static void d3dUnlock(PlayShared& s) { if (s.d3dlock) s.d3dlock->unlock(s.d3dlock->lock_ctx); }

// Copy a decoded D3D11 frame into its ring slot (called on the decode thread).
static bool ringStore(PlayShared& s, AVFrame* f, int64_t idx) {
    if (f->format != AV_PIX_FMT_D3D11) {
        // ponytail: sw-fallback upload not wired in the probe's play path; the
        // hw path is what step 4 proves. Degrade ladder handles sw in step 6.
        static bool once = false;
        if (!once) { fprintf(stderr, "play: non-d3d11 frame, skipping draw path\n"); once = true; }
        return false;
    }
    ID3D11Texture2D* src = (ID3D11Texture2D*)f->data[0];
    UINT slice = (UINT)(intptr_t)f->data[1];
    RingSlot& slot = s.ring[idx % RING];
    slot.frame.store(-1, std::memory_order_release);
    d3dLock(s);
    if (!slot.tex) {
        D3D11_TEXTURE2D_DESC sd; src->GetDesc(&sd);
        s.codedW = (int)sd.Width; s.codedH = (int)sd.Height;
        D3D11_TEXTURE2D_DESC td = {};
        td.Width = sd.Width; td.Height = sd.Height;
        td.MipLevels = 1; td.ArraySize = 1;
        td.Format = sd.Format; // NV12 (or P010 for 10-bit sources)
        td.SampleDesc.Count = 1;
        td.Usage = D3D11_USAGE_DEFAULT;
        td.BindFlags = D3D11_BIND_SHADER_RESOURCE;
        HRESULT hr = g_dev->CreateTexture2D(&td, nullptr, &slot.tex);
        if (FAILED(hr)) { d3dUnlock(s); fprintf(stderr, "ring CreateTexture2D hr=0x%08lx\n", (unsigned long)hr); return false; }
        D3D11_SHADER_RESOURCE_VIEW_DESC sv = {};
        sv.ViewDimension = D3D11_SRV_DIMENSION_TEXTURE2D;
        sv.Texture2D.MipLevels = 1;
        sv.Format = DXGI_FORMAT_R8_UNORM;
        g_dev->CreateShaderResourceView(slot.tex, &sv, &slot.srvY);
        sv.Format = DXGI_FORMAT_R8G8_UNORM;
        g_dev->CreateShaderResourceView(slot.tex, &sv, &slot.srvUV);
        if (!slot.srvY || !slot.srvUV) { d3dUnlock(s); fprintf(stderr, "ring SRV create failed\n"); return false; }
    }
    g_devctx->CopySubresourceRegion(slot.tex, 0, 0, 0, 0, src, slice, nullptr);
    d3dUnlock(s);
    slot.frame.store(idx, std::memory_order_release);
    return true;
}

// Decode thread: keep the ring filled around ps.want; reseek when it jumps.
static void decodeLoop(Decoder* d, PlayShared* s, AVRational fps, int64_t totalFrames) {
    AVPacket* pkt = av_packet_alloc();
    AVFrame*  f = av_frame_alloc();
    int64_t next = -1000000; // decoder's sequential position (frame it will produce next)
    while (!s->quit.load()) {
        int64_t want = s->want.load();
        int64_t lo = want - RING / 2; if (lo < 0) lo = 0;
        int64_t hi = want + RING / 2;
        if (totalFrames > 0 && hi >= totalFrames) hi = totalFrames - 1;

        if (next < lo || next > hi + 1) {
            // ring window jumped: frame-exact seek to lo, refill from there
            int64_t targetTs = d->startTs + av_rescale_q(lo, av_inv_q(fps), d->tb);
            int scanned = 0;
            int r = seekExact(*d, pkt, f, targetTs, fps, &scanned);
            if (r != 1) { WaitForSingleObject(s->wake, 20); continue; }
            ringStore(*s, f, lo);
            av_frame_unref(f);
            next = lo + 1;
            s->eofAt.store(INT64_MAX);
            continue;
        }
        if (next > hi) { // ring full ahead of playhead; sleep until it moves
            WaitForSingleObject(s->wake, 5);
            continue;
        }
        int r = nextFrame(*d, pkt, f);
        if (r == 0) { // EOF
            s->eofAt.store(next);
            next = INT64_MAX / 2; // force reseek on next backward jump
            WaitForSingleObject(s->wake, 20);
            continue;
        }
        if (r < 0) { Decoder::err("play decode", r); break; }
        ringStore(*s, f, next);
        av_frame_unref(f);
        next++;
    }
    av_packet_free(&pkt);
    av_frame_free(&f);
}

// --- tiny D3D11 present path ---

static const char* kShaderSrc =
"cbuffer cb : register(b0) { float2 uvScale; float2 pad; }\n"
"struct VSOut { float4 pos : SV_Position; float2 uv : TEXCOORD0; };\n"
"VSOut vsmain(uint id : SV_VertexID) {\n"
"  float2 p = float2((id << 1) & 2, id & 2);\n"
"  VSOut o;\n"
"  o.pos = float4(p * float2(2, -2) + float2(-1, 1), 0, 1);\n"
"  o.uv = p * uvScale;\n"
"  return o;\n"
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

struct Renderer {
    IDXGISwapChain* swap = nullptr;
    ID3D11RenderTargetView* rtv = nullptr;
    ID3D11VertexShader* vs = nullptr;
    ID3D11PixelShader* ps = nullptr;
    ID3D11SamplerState* samp = nullptr;
    ID3D11Buffer* cb = nullptr;
    int w = 0, h = 0;

    bool init(HWND hwnd, int cw, int ch) {
        w = cw; h = ch;
        IDXGIDevice* dxdev = nullptr;
        g_dev->QueryInterface(__uuidof(IDXGIDevice), (void**)&dxdev);
        IDXGIAdapter* ad = nullptr; dxdev->GetAdapter(&ad);
        IDXGIFactory1* fac = nullptr;
        ad->GetParent(__uuidof(IDXGIFactory1), (void**)&fac);
        DXGI_SWAP_CHAIN_DESC sd = {};
        sd.BufferDesc.Width = cw; sd.BufferDesc.Height = ch;
        sd.BufferDesc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        sd.SampleDesc.Count = 1;
        sd.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT;
        sd.BufferCount = 2;
        sd.OutputWindow = hwnd;
        sd.Windowed = TRUE;
        sd.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;
        HRESULT hr = fac->CreateSwapChain(g_dev, &sd, &swap);
        fac->Release(); ad->Release(); dxdev->Release();
        if (FAILED(hr)) { fprintf(stderr, "CreateSwapChain hr=0x%08lx\n", (unsigned long)hr); return false; }

        ID3D11Texture2D* bb = nullptr;
        swap->GetBuffer(0, __uuidof(ID3D11Texture2D), (void**)&bb);
        g_dev->CreateRenderTargetView(bb, nullptr, &rtv);
        bb->Release();

        ID3DBlob *vsb = nullptr, *psb = nullptr, *errb = nullptr;
        hr = D3DCompile(kShaderSrc, strlen(kShaderSrc), nullptr, nullptr, nullptr,
                        "vsmain", "vs_4_0", 0, 0, &vsb, &errb);
        if (FAILED(hr)) { fprintf(stderr, "vs compile: %s\n", errb ? (char*)errb->GetBufferPointer() : "?"); return false; }
        hr = D3DCompile(kShaderSrc, strlen(kShaderSrc), nullptr, nullptr, nullptr,
                        "psmain", "ps_4_0", 0, 0, &psb, &errb);
        if (FAILED(hr)) { fprintf(stderr, "ps compile: %s\n", errb ? (char*)errb->GetBufferPointer() : "?"); return false; }
        g_dev->CreateVertexShader(vsb->GetBufferPointer(), vsb->GetBufferSize(), nullptr, &vs);
        g_dev->CreatePixelShader(psb->GetBufferPointer(), psb->GetBufferSize(), nullptr, &ps);
        vsb->Release(); psb->Release();

        D3D11_SAMPLER_DESC smd = {};
        smd.Filter = D3D11_FILTER_MIN_MAG_MIP_LINEAR;
        smd.AddressU = smd.AddressV = smd.AddressW = D3D11_TEXTURE_ADDRESS_CLAMP;
        g_dev->CreateSamplerState(&smd, &samp);

        D3D11_BUFFER_DESC bd = {};
        bd.ByteWidth = 16;
        bd.Usage = D3D11_USAGE_DEFAULT;
        bd.BindFlags = D3D11_BIND_CONSTANT_BUFFER;
        g_dev->CreateBuffer(&bd, nullptr, &cb);
        return true;
    }

    void draw(PlayShared& s, ID3D11ShaderResourceView* srvY, ID3D11ShaderResourceView* srvUV,
              float uScale, float vScale) {
        d3dLock(s);
        float cbData[4] = { uScale, vScale, 0, 0 };
        g_devctx->UpdateSubresource(cb, 0, nullptr, cbData, 0, 0);
        D3D11_VIEWPORT vp = { 0, 0, (float)w, (float)h, 0, 1 };
        g_devctx->RSSetViewports(1, &vp);
        g_devctx->OMSetRenderTargets(1, &rtv, nullptr);
        g_devctx->IASetPrimitiveTopology(D3D11_PRIMITIVE_TOPOLOGY_TRIANGLELIST);
        g_devctx->IASetInputLayout(nullptr);
        g_devctx->VSSetShader(vs, nullptr, 0);
        g_devctx->VSSetConstantBuffers(0, 1, &cb);
        g_devctx->PSSetShader(ps, nullptr, 0);
        ID3D11ShaderResourceView* srvs[2] = { srvY, srvUV };
        g_devctx->PSSetShaderResources(0, 2, srvs);
        g_devctx->PSSetSamplers(0, 1, &samp);
        g_devctx->Draw(3, 0);
        ID3D11ShaderResourceView* nul[2] = { nullptr, nullptr };
        g_devctx->PSSetShaderResources(0, 2, nul);
        d3dUnlock(s);
        swap->Present(1, 0);
    }
};

static PlayShared* g_ps = nullptr; // for WndProc
static std::atomic<bool> g_playing{false};
static double  g_stepT0 = 0;       // key press time for latency measurement
static int64_t g_stepTarget = -1;
static double  g_playAnchorMs = 0; // QPC clock anchor (audio clock arrives in step 5)
static int64_t g_playAnchorFrame = 0;
static int64_t g_totalFrames = 0;

static LRESULT CALLBACK playWndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    if (m == WM_DESTROY) { PostQuitMessage(0); return 0; }
    if (m == WM_KEYDOWN && g_ps) {
        int64_t cur = g_ps->want.load();
        int64_t nw = cur;
        switch (w) {
        case VK_RIGHT: nw = cur + 1; break;
        case VK_LEFT:  nw = cur - 1; break;
        case VK_NEXT:  nw = cur + 30; break;
        case VK_PRIOR: nw = cur - 30; break;
        case VK_SPACE:
            g_playing = !g_playing;
            if (g_playing) { g_playAnchorMs = nowMs(); g_playAnchorFrame = cur; }
            printf("%s at frame %lld\n", g_playing ? "PLAY" : "PAUSE", (long long)cur);
            return 0;
        case VK_ESCAPE: PostQuitMessage(0); return 0;
        default: return DefWindowProcW(h, m, w, l);
        }
        if (nw < 0) nw = 0;
        if (g_totalFrames > 0 && nw >= g_totalFrames) nw = g_totalFrames - 1;
        if (nw != cur) {
            g_playing = false;
            g_stepT0 = nowMs();
            g_stepTarget = nw;
            g_ps->want.store(nw);
            SetEvent(g_ps->wake);
        }
        return 0;
    }
    return DefWindowProcW(h, m, w, l);
}

static int cmdPlay(const char* path, int exitAfterSec) {
    if (!createD3D11Device()) return 2;
    AVBufferRef* hwdev = createHwDeviceFromD3D();
    if (!hwdev) { fprintf(stderr, "no hw device - --play needs the hw path\n"); return 2; }

    Decoder d;
    if (!d.open(path, hwdev)) return 2;
    AVStream* st = d.fmt->streams[d.vstream];
    AVRational fps = st->avg_frame_rate.num ? st->avg_frame_rate : AVRational{30000, 1001};
    g_totalFrames = st->nb_frames > 0 ? st->nb_frames
                  : (int64_t)(d.fmt->duration / (double)AV_TIME_BASE * av_q2d(fps));
    printf("play %s: %dx%d fps=%d/%d frames=%lld\n", path, d.width, d.height,
           fps.num, fps.den, (long long)g_totalFrames);

    PlayShared ps;
    ps.wake = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    AVHWDeviceContext* hctx = (AVHWDeviceContext*)hwdev->data;
    ps.d3dlock = (AVD3D11VADeviceContext*)hctx->hwctx;
    g_ps = &ps;

    // window sized to video aspect, longest side 960
    int cw, ch;
    if (d.width >= d.height) { cw = 960; ch = (int)(960.0 * d.height / d.width); }
    else                     { ch = 960; cw = (int)(960.0 * d.width / d.height); }
    WNDCLASSW wc = {};
    wc.lpfnWndProc = playWndProc;
    wc.hInstance = GetModuleHandleW(nullptr);
    wc.lpszClassName = L"BeckyEngineProbe";
    wc.hCursor = LoadCursorW(nullptr, (LPCWSTR)IDC_ARROW);
    RegisterClassW(&wc);
    RECT r = { 0, 0, cw, ch };
    AdjustWindowRect(&r, WS_OVERLAPPEDWINDOW, FALSE);
    HWND hwnd = CreateWindowW(L"BeckyEngineProbe", L"becky-engine-probe",
                              WS_OVERLAPPEDWINDOW | WS_VISIBLE,
                              CW_USEDEFAULT, CW_USEDEFAULT,
                              r.right - r.left, r.bottom - r.top,
                              nullptr, nullptr, wc.hInstance, nullptr);
    if (!hwnd) { fprintf(stderr, "CreateWindow failed\n"); return 2; }

    Renderer rend;
    if (!rend.init(hwnd, cw, ch)) return 2;

    std::thread dec(decodeLoop, &d, &ps, fps, g_totalFrames);

    int64_t lastDrawn = -1;
    ID3D11ShaderResourceView *lastY = nullptr, *lastUV = nullptr;
    int frames60 = 0; double statT0 = nowMs(); double runT0 = nowMs();
    FILETIME ftc, fte, ftk0, ftu0, ftk1, ftu1;
    GetProcessTimes(GetCurrentProcess(), &ftc, &fte, &ftk0, &ftu0);

    MSG msg;
    bool done = false;
    while (!done) {
        while (PeekMessageW(&msg, nullptr, 0, 0, PM_REMOVE)) {
            if (msg.message == WM_QUIT) { done = true; break; }
            TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
        if (done) break;

        if (g_playing) {
            double el = (nowMs() - g_playAnchorMs) / 1000.0;
            int64_t due = g_playAnchorFrame + (int64_t)(el * av_q2d(fps));
            if (g_totalFrames > 0 && due >= g_totalFrames) { due = g_totalFrames - 1; g_playing = false; }
            if (due != ps.want.load()) { ps.want.store(due); SetEvent(ps.wake); }
        }

        int64_t want = ps.want.load();
        RingSlot& slot = ps.ring[want % RING];
        ID3D11ShaderResourceView *dy = lastY, *duv = lastUV;
        int64_t drawn = lastDrawn;
        if (slot.frame.load(std::memory_order_acquire) == want && slot.srvY) {
            dy = slot.srvY; duv = slot.srvUV; drawn = want;
        }
        if (dy) {
            float us = ps.codedW ? (float)d.width / ps.codedW : 1.0f;
            float vs = ps.codedH ? (float)d.height / ps.codedH : 1.0f;
            rend.draw(ps, dy, duv, us, vs);
            if (drawn != lastDrawn) {
                lastDrawn = drawn; lastY = dy; lastUV = duv;
                if (drawn == g_stepTarget) {
                    printf("step to %lld latency=%.2f ms\n", (long long)drawn, nowMs() - g_stepT0);
                    g_stepTarget = -1;
                }
            }
            frames60++;
        } else {
            Sleep(2); // nothing decoded yet
        }

        if (nowMs() - statT0 > 1000.0) {
            printf("stat: drawn=%lld want=%lld fps=%.1f playing=%d\n",
                   (long long)lastDrawn, (long long)want,
                   frames60 * 1000.0 / (nowMs() - statT0), g_playing ? 1 : 0);
            fflush(stdout);
            frames60 = 0; statT0 = nowMs();
        }
        if (exitAfterSec > 0 && nowMs() - runT0 > exitAfterSec * 1000.0) done = true;
    }

    GetProcessTimes(GetCurrentProcess(), &ftc, &fte, &ftk1, &ftu1);
    auto ftMs = [](const FILETIME& a, const FILETIME& b) {
        ULARGE_INTEGER ua { { a.dwLowDateTime, a.dwHighDateTime } };
        ULARGE_INTEGER ub { { b.dwLowDateTime, b.dwHighDateTime } };
        return (double)(ub.QuadPart - ua.QuadPart) / 10000.0;
    };
    double wallMs = nowMs() - runT0;
    double cpuMs = ftMs(ftk0, ftk1) + ftMs(ftu0, ftu1);
    printf("cpu: %.1f%% of one core over %.1f s (process total, all threads)\n",
           cpuMs * 100.0 / wallMs, wallMs / 1000.0);

    ps.quit = true;
    SetEvent(ps.wake);
    dec.join();
    d.close();
    av_buffer_unref(&hwdev);
    return 0;
}

// ---------------------------------------------------------------- arg parsing

static AVRational parseFps(const char* s) {
    int num = 0, den = 1;
    if (sscanf(s, "%d/%d", &num, &den) < 1 || num <= 0 || den <= 0) {
        fprintf(stderr, "bad --fps '%s'\n", s);
        return {30000, 1001};
    }
    return {num, den};
}

static std::vector<int64_t> parseTargets(const char* s) {
    std::vector<int64_t> out;
    const char* p = s;
    while (*p) {
        char* end = nullptr;
        long long v = strtoll(p, &end, 10);
        if (end == p) break;
        out.push_back(v);
        p = (*end == ',') ? end + 1 : end;
    }
    return out;
}

int main(int argc, char** argv) {
    const char* mode = nullptr;
    const char* file = nullptr;
    int frames = 300;
    AVRational fps = {30000, 1001};
    std::vector<int64_t> targets = {100, 101, 102, 1000, 4499};
    bool reportSync = false;
    int exitAfter = 0;

    for (int i = 1; i < argc; i++) {
        std::string a = argv[i];
        auto next = [&]() -> const char* { return (i + 1 < argc) ? argv[++i] : ""; };
        if (a == "--decode")      { mode = "decode"; file = next(); }
        else if (a == "--seek-test") { mode = "seek"; file = next(); }
        else if (a == "--play")      { mode = "play"; file = next(); }
        else if (a == "--play-reel") { mode = "reel"; file = next(); }
        else if (a == "--frames")    frames = atoi(next());
        else if (a == "--fps")       fps = parseFps(next());
        else if (a == "--targets")   targets = parseTargets(next());
        else if (a == "--report-sync") reportSync = true;
        else if (a == "--exit-after")  exitAfter = atoi(next());
        else { fprintf(stderr, "unknown arg %s\n", a.c_str()); return 2; }
    }
    (void)reportSync;
    if (!mode || !file) {
        fprintf(stderr, "usage: becky-engine-probe --decode|--seek-test|--play|--play-reel <file> "
                        "[--frames N] [--fps A/B] [--targets a,b,c] [--report-sync]\n");
        return 2;
    }
    av_log_set_level(getenv("PROBE_DEBUG") ? AV_LOG_DEBUG : AV_LOG_WARNING);

    if (!strcmp(mode, "decode")) return cmdDecode(file, frames);
    if (!strcmp(mode, "seek"))   return cmdSeekTest(file, fps, targets);
    if (!strcmp(mode, "play"))   return cmdPlay(file, exitAfter);
    fprintf(stderr, "mode %s not implemented yet (step 5 pending)\n", mode);
    return 2;
}
