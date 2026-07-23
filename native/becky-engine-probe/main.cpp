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
#include <d3d10.h> // ID3D10Multithread (works on the d3d11 immediate context)
#include <dxgi.h>
#include <dxgi1_3.h> // waitable swapchain (IDXGIFactory2 / IDXGISwapChain2)
#include <d3dcompiler.h>
#include <cstdio>
#include <cstdint>
#include <cstring>
#include <cstdlib>
#include <string>
#include <vector>
#include <atomic>
#include <thread>
#include <algorithm>
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
#include <portaudio.h>

static double nowMs() {
    static LARGE_INTEGER freq = { };
    if (!freq.QuadPart) QueryPerformanceFrequency(&freq);
    LARGE_INTEGER t; QueryPerformanceCounter(&t);
    return (double)t.QuadPart * 1000.0 / (double)freq.QuadPart;
}

// ---------------------------------------------------------------- D3D11 device

static ID3D11Device*        g_dev = nullptr;    // RENDER device (window/swapchain)
static ID3D11DeviceContext* g_devctx = nullptr;
static ID3D11Device*        g_decDev = nullptr; // DECODE device - separate so a
static ID3D11DeviceContext* g_decCtx = nullptr; // blocking Present can never
                                                // stall decode (measured: Present
                                                // holds the protection CS ~45ms
                                                // and dropped decode to 15fps
                                                // when devices were shared)

// On this machine the DEFAULT adapter is "Microsoft Basic Render Driver"
// (software, no ID3D11VideoDevice) and the RTX 3070 is adapter 1 - so never
// create with a NULL adapter. Enumerate and take the first adapter whose
// device exposes ID3D11VideoDevice (measured 2026-07-22, diag_adapters.cpp).
static bool createOneDevice(IDXGIAdapter1* ad, ID3D11Device** dev, ID3D11DeviceContext** ctx) {
    D3D_FEATURE_LEVEL fl;
    HRESULT hr = D3D11CreateDevice(ad, D3D_DRIVER_TYPE_UNKNOWN, nullptr,
                                   D3D11_CREATE_DEVICE_BGRA_SUPPORT |
                                   D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                                   nullptr, 0, D3D11_SDK_VERSION, dev, &fl, ctx);
    if (FAILED(hr)) return false;
    // Per-call serialization inside each device (decode thread vs any other
    // call). Without it the NVIDIA UMD races and segfaults (nvwgf2umx.dll).
    ID3D10Multithread* mt = nullptr;
    if (SUCCEEDED((*ctx)->QueryInterface(__uuidof(ID3D10Multithread), (void**)&mt))) {
        mt->SetMultithreadProtected(TRUE);
        mt->Release();
    }
    return true;
}

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
        if (!createOneDevice(ad, &dev, &ctx)) { ad->Release(); continue; }
        ID3D11VideoDevice* vd = nullptr;
        if (SUCCEEDED(dev->QueryInterface(__uuidof(ID3D11VideoDevice), (void**)&vd))) {
            vd->Release();
            g_dev = dev; g_devctx = ctx;
            // Decode device on the SAME adapter; falls back to sharing the
            // render device if a second create fails.
            if (!createOneDevice(ad, &g_decDev, &g_decCtx)) {
                g_decDev = g_dev; g_decCtx = g_devctx;
                g_decDev->AddRef(); g_decCtx->AddRef();
                fprintf(stderr, "warning: single-device mode (decode contends with Present)\n");
            }
            fprintf(stderr, "d3d11 render+decode devices on adapter %u: %ls\n", i, desc.Description);
            ad->Release();
            fac->Release();
            return true;
        }
        ctx->Release(); dev->Release();
        ad->Release();
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
    ID3D11Device* dd = g_decDev ? g_decDev : g_dev; // decoder lives on the decode device
    dd->AddRef();
    d3d->device = dd;
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
    AVD3D11VADeviceContext* hwLock = nullptr; // set by --play; serializes flushes

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
            ctx->extra_hw_frames = 6; // headroom for the 4-frame delay-unref queue
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
// If abortWant is given: abort (-3) only when the CURRENT want leaves the
// +/-15 window this seek serves. Exact-match aborting livelocked playback
// (want advances every 33 ms; no ~250 ms keyframe walk could ever finish -
// measured 2026-07-23, video froze at frame 18 while audio ran). Window-based
// aborting keeps latest-chasing for real jumps but lets useful walks land.
static const int RING = 31; // ring slots, +/-15 around the playhead
static const int SEEK_ABORTED = -3;
static int seekExact(Decoder& d, AVPacket* pkt, AVFrame* f,
                     int64_t targetTs, AVRational fps, int* framesScanned,
                     const std::atomic<int64_t>* abortWant = nullptr,
                     int64_t targetFrame = 0) {
    int64_t frameDurTb = av_rescale_q(1, av_inv_q(fps), d.tb);
    if (frameDurTb <= 0) frameDurTb = 1;
    int r = av_seek_frame(d.fmt, d.vstream, targetTs, AVSEEK_FLAG_BACKWARD);
    if (r < 0) { Decoder::err("av_seek_frame", r); return r; }
    // flush releases decoder pool surfaces; serialize it against draws/copies
    // (part of the nvwgf2umx crash fix, 2026-07-23)
    if (d.hwLock) { d.hwLock->lock(d.hwLock->lock_ctx); avcodec_flush_buffers(d.ctx); d.hwLock->unlock(d.hwLock->lock_ctx); }
    else avcodec_flush_buffers(d.ctx);
    int scanned = 0;
    for (;;) {
        if (abortWant) {
            int64_t w = abortWant->load(std::memory_order_relaxed);
            if (w < targetFrame - RING / 2 || w > targetFrame + RING / 2)
                return SEEK_ABORTED;
        }
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



struct RingSlot {
    std::atomic<int64_t> frame{-1};
    ID3D11Texture2D* decTex = nullptr;  // decode-device side (copy target, shared)
    IDXGIKeyedMutex* kmDec = nullptr;
    ID3D11Texture2D* rndTex = nullptr;  // render-device view of the same texture
    IDXGIKeyedMutex* kmRnd = nullptr;
    ID3D11ShaderResourceView* srvY = nullptr;  // on the RENDER device
    ID3D11ShaderResourceView* srvUV = nullptr;
};

struct PlayShared {
    RingSlot ring[RING];
    std::atomic<int64_t> want{0};      // playhead frame the UI wants shown
    std::atomic<int64_t> wantStampMs{0}; // when want last changed (nowMs)
    std::atomic<bool>    quit{false};
    std::atomic<int64_t> eofAt{INT64_MAX}; // first frame index past EOF, if hit
    HANDLE wake = nullptr;             // decode thread wake-up
    AVD3D11VADeviceContext* d3dlock = nullptr; // FFmpeg's device lock: serialize ctx use
    std::atomic<int> codedW{0}, codedH{0}; // decoder texture dims (may exceed display);
                                            // written by the decode thread, read by the
                                            // UI thread with no other lock - was a plain
                                            // int (a real data race under -O2), found while
                                            // chasing an intermittent storm-mode crash.
    // instrumentation (decode-side truth for the storm reports)
    std::atomic<int> seeksDone{0}, seeksAborted{0}, framesDecoded{0}, backfills{0};
};

// Single entry point for moving the playhead: store + stamp + wake.
static void setWant(PlayShared& s, int64_t frame) {
    s.want.store(frame);
    s.wantStampMs.store((int64_t)nowMs());
    SetEvent(s.wake);
}

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
    if (!slot.decTex) {
        D3D11_TEXTURE2D_DESC sd; src->GetDesc(&sd);
        s.codedW = (int)sd.Width; s.codedH = (int)sd.Height;
        D3D11_TEXTURE2D_DESC td = {};
        td.Width = sd.Width; td.Height = sd.Height;
        td.MipLevels = 1; td.ArraySize = 1;
        td.Format = sd.Format; // NV12 (or P010 for 10-bit sources)
        td.SampleDesc.Count = 1;
        td.Usage = D3D11_USAGE_DEFAULT;
        td.BindFlags = D3D11_BIND_SHADER_RESOURCE;
        // Legacy SHARED (no keyed mutex): the mutex costs a full GPU sync per
        // acquire (~78 ms measured with DWM in the loop) and throttled decode
        // to 30 fps. Ordering = decode-side Flush + the slot.frame atomic.
        // ponytail: worst case is one torn frame for one present; upgrade path
        // is NT-handle sharing + ID3D11Fence if it ever shows on screen.
        td.MiscFlags = D3D11_RESOURCE_MISC_SHARED;
        HRESULT hr = g_decDev->CreateTexture2D(&td, nullptr, &slot.decTex);
        if (FAILED(hr)) { fprintf(stderr, "ring CreateTexture2D hr=0x%08lx\n", (unsigned long)hr); return false; }
        IDXGIResource* res = nullptr; HANDLE sh = nullptr;
        slot.decTex->QueryInterface(__uuidof(IDXGIResource), (void**)&res);
        if (res) { res->GetSharedHandle(&sh); res->Release(); }
        if (!sh || FAILED(g_dev->OpenSharedResource(sh, __uuidof(ID3D11Texture2D), (void**)&slot.rndTex))) {
            fprintf(stderr, "ring OpenSharedResource failed\n"); return false;
        }
        D3D11_SHADER_RESOURCE_VIEW_DESC sv = {};
        sv.ViewDimension = D3D11_SRV_DIMENSION_TEXTURE2D;
        sv.Texture2D.MipLevels = 1;
        sv.Format = DXGI_FORMAT_R8_UNORM;
        g_dev->CreateShaderResourceView(slot.rndTex, &sv, &slot.srvY);
        sv.Format = DXGI_FORMAT_R8G8_UNORM;
        g_dev->CreateShaderResourceView(slot.rndTex, &sv, &slot.srvUV);
        if (!slot.srvY || !slot.srvUV) { fprintf(stderr, "ring SRV create failed\n"); return false; }
    }
    // Copy on the DECODE device's context: zero contention with Present.
    g_decCtx->CopySubresourceRegion(slot.decTex, 0, 0, 0, 0, src, slice, nullptr);
    g_decCtx->Flush(); // submit before publishing the slot
    slot.frame.store(idx, std::memory_order_release);
    return true;
}

// Decode thread: keep the ring filled around ps.want; reseek when it jumps.
static void decodeLoop(Decoder* d, PlayShared* s, AVRational fps, int64_t totalFrames) {
    AVPacket* pkt = av_packet_alloc();
    AVFrame*  f = av_frame_alloc();
    // Delay-unref queue: our ring copies are ASYNC GPU commands; unreffing the
    // AVFrame immediately returns its pool slice to the decoder, which can
    // rewrite it before the queued copy executes (nvwgf2umx segfault on the
    // all-intra proxy whose decode rate is ~10x realtime). Hold the last 4
    // frames' refs so in-flight copies always read stable memory.
    AVFrame* held[4] = {};
    int heldIdx = 0;
    auto holdFrame = [&](AVFrame* src) {
        if (!held[heldIdx]) held[heldIdx] = av_frame_alloc();
        av_frame_unref(held[heldIdx]);
        av_frame_move_ref(held[heldIdx], src);
        heldIdx = (heldIdx + 1) % 4;
    };
    int64_t next = -1000000; // decoder's sequential position (frame it will produce next)
    while (!s->quit.load()) {
        int64_t want = s->want.load();
        int64_t lo = want - RING / 2; if (lo < 0) lo = 0;
        int64_t hi = want + RING / 2;
        if (totalFrames > 0 && hi >= totalFrames) hi = totalFrames - 1;

        // A small forward lag (decode briefly behind during playback) is
        // caught up by plain sequential decoding - NVDEC decodes far faster
        // than realtime, and a BACKWARD-flag reseek inside the same GOP would
        // land on the SAME keyframe and be slower. Reseek only on real jumps.
        if ((next < lo && want - next >= 60) || next > hi + 1 || next < 0) {
            // Ring window jumped: seek straight to WANT and store it FIRST so
            // the paint latency is one seek + one store, never a 15-frame
            // backfill. Abortable: if want leaves this seek's +/-15 window,
            // drop the stale walk and chase the new target.
            int64_t targetTs = d->startTs + av_rescale_q(want, av_inv_q(fps), d->tb);
            int scanned = 0;
            int r = seekExact(*d, pkt, f, targetTs, fps, &scanned, &s->want, want);
            if (r == SEEK_ABORTED) { s->seeksAborted++; next = -1000000; continue; }
            if (r != 1) { WaitForSingleObject(s->wake, 20); continue; }
            s->seeksDone++; s->framesDecoded += scanned;
            ringStore(*s, f, want);
            holdFrame(f);
            next = want + 1;
            s->eofAt.store(INT64_MAX);
            continue;
        }
        if (next > hi) {
            // Forward window full. Backfill [lo, want-1] so stepping BACK is a
            // ring hit - but ONLY once the playhead has been still for 150 ms
            // (backfill during a storm steals the decoder from latest-chasing;
            // measured: it collapsed far-seek paints to 1/30).
            int64_t missing = -1;
            if ((int64_t)nowMs() - s->wantStampMs.load() > 150) {
                for (int64_t i = lo; i < want; i++) {
                    if (s->ring[i % RING].frame.load(std::memory_order_acquire) != i) { missing = i; break; }
                }
            }
            if (missing >= 0) {
                int64_t targetTs = d->startTs + av_rescale_q(missing, av_inv_q(fps), d->tb);
                int scanned = 0;
                int r = seekExact(*d, pkt, f, targetTs, fps, &scanned, &s->want, want);
                if (r == SEEK_ABORTED) { next = -1000000; continue; }
                if (r == 1) {
                    s->backfills++;
                    ringStore(*s, f, missing);
                    holdFrame(f);
                    next = missing + 1; // sequential fill continues from here
                    // ponytail: frames [want, hi] get re-decoded on the way back
                    // through - idle-time waste only, paint already happened.
                    continue;
                }
            }
            WaitForSingleObject(s->wake, 5);
            continue;
        }
        int r = nextFrame(*d, pkt, f);
        if (r == 1) s->framesDecoded++;
        if (r == 0) { // EOF
            s->eofAt.store(next);
            next = INT64_MAX / 2; // force reseek on next backward jump
            WaitForSingleObject(s->wake, 20);
            continue;
        }
        if (r < 0) { Decoder::err("play decode", r); break; }
        if (next >= lo) { ringStore(*s, f, next); holdFrame(f); } // below lo: decode-and-discard
        else av_frame_unref(f);
        next++;
    }
    for (int i = 0; i < 4; i++) if (held[i]) av_frame_free(&held[i]);
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
    IDXGISwapChain1* swap = nullptr;
    HANDLE frameWait = nullptr; // waitable-swapchain vsync gate
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
        IDXGIFactory2* fac = nullptr;
        ad->GetParent(__uuidof(IDXGIFactory2), (void**)&fac);
        // WAITABLE swapchain: the vsync wait happens on frameWait at the loop
        // top, with NO lock held, and Present(0,0) never blocks. A blocking
        // Present(1,0) held the ID3D10Multithread CS for its whole ~60 ms
        // wait and starved the decoder to ~16 fps (measured 2026-07-23).
        DXGI_SWAP_CHAIN_DESC1 sd = {};
        sd.Width = cw; sd.Height = ch;
        sd.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
        sd.SampleDesc.Count = 1;
        sd.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT;
        sd.BufferCount = 2;
        sd.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;
        sd.Flags = DXGI_SWAP_CHAIN_FLAG_FRAME_LATENCY_WAITABLE_OBJECT;
        HRESULT hr = fac->CreateSwapChainForHwnd(g_dev, hwnd, &sd, nullptr, nullptr, &swap);
        fac->Release(); ad->Release(); dxdev->Release();
        if (FAILED(hr)) { fprintf(stderr, "CreateSwapChainForHwnd hr=0x%08lx\n", (unsigned long)hr); return false; }
        IDXGISwapChain2* sw2 = nullptr;
        if (SUCCEEDED(swap->QueryInterface(__uuidof(IDXGISwapChain2), (void**)&sw2))) {
            sw2->SetMaximumFrameLatency(1);
            frameWait = sw2->GetFrameLatencyWaitableObject();
            sw2->Release();
        }
        if (!frameWait) fprintf(stderr, "warning: no frame-latency waitable, pacing by Sleep\n");

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

    double lastLockMs = 0, lastDrawMs = 0, lastPresentMs = 0; // diagnostics

    // Returns false if the slot was busy (decode writing it) - caller skips.
    bool draw(IDXGIKeyedMutex* km, ID3D11ShaderResourceView* srvY, ID3D11ShaderResourceView* srvUV,
              float uScale, float vScale) {
        double t0 = nowMs();
        if (km && km->AcquireSync(0, 2) != S_OK) return false; // busy: keep last image
        double t1 = nowMs(); lastLockMs = t1 - t0;
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
        double t2 = nowMs(); lastDrawMs = t2 - (t0 + lastLockMs);
        if (km) km->ReleaseSync(0);
        // Non-blocking present; pacing is the frameWait gate in the loop.
        // Present now only ever stalls the RENDER device - decode runs free.
        swap->Present(0, 0);
        lastPresentMs = nowMs() - t2;
        return true;
    }
};

// --- storm harness: measured proof that latest-chasing never blocks the UI ---
// Phases: A sequential +1 steps @30/s, B far random seeks @3/s,
//         C scrub storm @25 req/s for 30 s (wander +/-60 frames, 10% far jump).
struct StormStats {
    std::vector<double> lat;   // paint latency of requests that got painted
    int requests = 0;
    int painted = 0;
    void report(const char* name) {
        std::vector<double> v = lat;
        std::sort(v.begin(), v.end());
        double med = v.empty() ? 0 : v[v.size() / 2];
        double p95 = v.empty() ? 0 : v[(size_t)(v.size() * 0.95)];
        double mx  = v.empty() ? 0 : v.back();
        printf("storm[%s]: requests=%d painted=%d superseded=%d latency_ms median=%.1f p95=%.1f max=%.1f\n",
               name, requests, painted, requests - painted, med, p95, mx);
    }
};

struct Storm {
    bool     active = false;
    int      phase = 0;          // 0=A seq, 1=B far, 2=C storm, 3=done
    int      issued = 0;
    double   nextReqAt = 0;
    double   phaseT0 = 0;
    int64_t  lastReqFrame = -1;
    double   lastReqT = 0;
    int      lastReqPhase = 0;
    bool     lastPainted = true;
    uint32_t rng = 0xBECC1;
    StormStats stats[3];
    double   uiMaxGap = 0;
    double   lastLoopT = 0;
    uint32_t rnd() { rng = rng * 1664525u + 1013904223u; return rng; }
};
static Storm g_storm;

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
            setWant(*g_ps, nw);
        }
        return 0;
    }
    return DefWindowProcW(h, m, w, l);
}

// ---------------------------------------------------------------- step 5: audio
// WASAPI output via PortAudio, the same library+pattern proven working on this
// machine by native/audio-host (audio_device.cpp): Pa_OpenStream on the default
// device (which resolves to WASAPI here), a realtime pull callback, silence on
// underrun. This is the "PORT the working WASAPI output" instruction - reusing
// the proven library beats hand-rolling raw IAudioClient tonight.
//
// Audio is decoded on its own thread into a small SPSC float ring; the audio
// device's own callback clock (frames actually pulled) becomes the ONE clock
// `--play` schedules video against while playing - per spec section 2/7 risk 1,
// this is what makes video unable to drift from audio: it IS the audio clock.

struct AudioRing {
    static const int64_t CAP = 48000 * 2 * 4; // ~4s stereo float @48kHz, interleaved samples
    std::vector<float> buf;
    std::atomic<int64_t> w{0}, r{0}; // interleaved-sample counters (monotonic)
    AudioRing() : buf(CAP, 0.0f) {}
    int64_t writable() const { return CAP - (w.load(std::memory_order_relaxed) - r.load(std::memory_order_relaxed)); }
    void push(const float* src, int64_t n) {
        int64_t ww = w.load(std::memory_order_relaxed);
        for (int64_t i = 0; i < n; i++) buf[(ww + i) % CAP] = src[i];
        w.store(ww + n, std::memory_order_release);
    }
    // Fills n interleaved samples into dst; missing tail is silence. Returns samples supplied from real audio.
    int64_t pull(float* dst, int64_t n) {
        int64_t ww = w.load(std::memory_order_acquire), rr = r.load(std::memory_order_relaxed);
        int64_t avail = ww - rr;
        int64_t take = avail < n ? avail : n;
        if (take < 0) take = 0;
        for (int64_t i = 0; i < take; i++) dst[i] = buf[(rr + i) % CAP];
        for (int64_t i = take; i < n; i++) dst[i] = 0.0f;
        r.store(rr + take, std::memory_order_release);
        return take;
    }
};

struct AudioDecoder {
    AVCodecContext* actx = nullptr;
    int astream = -1;
    int outRate = 48000;
    int outCh = 2;
    SwrContext* swr = nullptr;

    bool open(AVFormatContext* fmt) {
        astream = av_find_best_stream(fmt, AVMEDIA_TYPE_AUDIO, -1, -1, nullptr, 0);
        if (astream < 0) return false;
        AVStream* st = fmt->streams[astream];
        const AVCodec* dec = avcodec_find_decoder(st->codecpar->codec_id);
        if (!dec) return false;
        actx = avcodec_alloc_context3(dec);
        if (!actx) return false;
        avcodec_parameters_to_context(actx, st->codecpar);
        if (avcodec_open2(actx, dec, nullptr) < 0) { avcodec_free_context(&actx); return false; }
        AVChannelLayout outLayout = AV_CHANNEL_LAYOUT_STEREO;
        int r = swr_alloc_set_opts2(&swr, &outLayout, AV_SAMPLE_FMT_FLT, outRate,
                                     &actx->ch_layout, actx->sample_fmt, actx->sample_rate,
                                     0, nullptr);
        if (r < 0 || !swr || swr_init(swr) < 0) { close(); return false; }
        return true;
    }
    void close() {
        if (swr) swr_free(&swr);
        if (actx) avcodec_free_context(&actx);
        astream = -1;
    }
};

static std::atomic<int64_t> g_audioFramesConsumed{0}; // device-callback clock, sample-FRAMES (not interleaved)
static std::atomic<int64_t> g_audioUnderrunFrames{0}; // frames the ring could not supply (silence filled)
static std::atomic<int64_t> g_audioCallbacks{0};
static AudioRing* g_audioRing = nullptr;

static int paPullCallback(const void*, void* output, unsigned long frameCount,
                          const PaStreamCallbackTimeInfo*, PaStreamCallbackFlags, void*) {
    float* out = (float*)output;
    int64_t want = (int64_t)frameCount * 2; // stereo interleaved
    int64_t got = g_audioRing ? g_audioRing->pull(out, want) : 0;
    if (got < want) g_audioUnderrunFrames += (want - got) / 2;
    g_audioFramesConsumed += frameCount;
    g_audioCallbacks++;
    return paContinue;
}

// Decode thread: pulls audio packets from the SAME AVFormatContext the video
// decoder reads (single demuxer, both streams interleaved in one file) and
// pushes resampled float stereo into the ring. Sleeps when the ring is full so
// a stalled audio device (or none) cannot spin this thread at 100%.
static void audioDecodeLoop(AVFormatContext* fmt, AudioDecoder* ad, AudioRing* ring, std::atomic<bool>* quit) {
    AVPacket* pkt = av_packet_alloc();
    AVFrame*  f = av_frame_alloc();
    std::vector<float> tmp;
    for (;;) {
        if (quit->load(std::memory_order_relaxed)) break;
        if (ring->writable() < 4096) { Sleep(2); continue; }
        int r = avcodec_receive_frame(ad->actx, f);
        if (r == AVERROR(EAGAIN)) {
            int rr = av_read_frame(fmt, pkt);
            if (rr < 0) { Sleep(5); continue; } // EOF: idle (single-file probe, no loop)
            if (pkt->stream_index != ad->astream) { av_packet_unref(pkt); continue; }
            avcodec_send_packet(ad->actx, pkt);
            av_packet_unref(pkt);
            continue;
        }
        if (r < 0) { Sleep(5); continue; }
        int64_t outSamples = swr_get_out_samples(ad->swr, f->nb_samples);
        if (outSamples <= 0) { av_frame_unref(f); continue; }
        if ((int64_t)tmp.size() < outSamples * ad->outCh) tmp.resize(outSamples * ad->outCh);
        uint8_t* outPtrs[1] = { (uint8_t*)tmp.data() };
        int got = swr_convert(ad->swr, outPtrs, (int)outSamples, (const uint8_t**)f->data, f->nb_samples);
        if (got > 0) ring->push(tmp.data(), (int64_t)got * ad->outCh);
        av_frame_unref(f);
    }
    av_packet_free(&pkt);
    av_frame_free(&f);
}

static int cmdPlay(const char* path, int exitAfterSec, bool storm, bool reportSync) {
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

    // step 5: audio. Opened from the SAME AVFormatContext (one demuxer, two
    // streams) so packets for both are read from one av_read_frame stream by
    // whichever thread gets there first; each thread only consumes its own
    // stream index and leaves the other's packets for the other thread's next
    // av_read_frame call (FFmpeg's demuxer is safe to call from >1 thread only
    // if serialized - so audioDecodeLoop and decodeLoop's own av_read_frame
    // calls must not race). To avoid that hazard the video decode thread reads
    // ONLY from its own private AVFormatContext (Decoder::open already does
    // this per source), so a second, independent avformat_open_input for audio
    // is used instead of sharing d.fmt.
    AVFormatContext* afmt = nullptr;
    AudioDecoder ad;
    AudioRing aring;
    std::atomic<bool> audioQuit{false};
    std::thread audioThread;
    bool haveAudio = false;
    bool paStarted = false;
    bool paInited = false;
    PaStream* paStream = nullptr;
    if (avformat_open_input(&afmt, path, nullptr, nullptr) == 0 &&
        avformat_find_stream_info(afmt, nullptr) >= 0 &&
        ad.open(afmt)) {
        haveAudio = true;
        g_audioRing = &aring;
        audioThread = std::thread(audioDecodeLoop, afmt, &ad, &aring, &audioQuit);
        PaError paErr = Pa_Initialize();
        if (paErr == paNoError) {
            paInited = true;
            PaStreamParameters outp{};
            outp.device = Pa_GetDefaultOutputDevice();
            if (outp.device != paNoDevice) {
                const PaDeviceInfo* info = Pa_GetDeviceInfo(outp.device);
                outp.channelCount = 2;
                outp.sampleFormat = paFloat32;
                outp.suggestedLatency = info ? info->defaultLowOutputLatency : 0.05;
                paErr = Pa_OpenStream(&paStream, nullptr, &outp, ad.outRate, 256,
                                      paClipOff, paPullCallback, nullptr);
                if (paErr == paNoError) {
                    paErr = Pa_StartStream(paStream);
                    if (paErr == paNoError) {
                        paStarted = true;
                        printf("audio: %s @ %d Hz stereo (WASAPI via PortAudio)\n",
                               info && info->name ? info->name : "?", ad.outRate);
                    } else {
                        fprintf(stderr, "audio: Pa_StartStream failed: %s\n", Pa_GetErrorText(paErr));
                    }
                } else {
                    fprintf(stderr, "audio: Pa_OpenStream failed: %s\n", Pa_GetErrorText(paErr));
                }
            } else {
                fprintf(stderr, "audio: no default output device\n");
            }
        } else {
            fprintf(stderr, "audio: Pa_Initialize failed: %s\n", Pa_GetErrorText(paErr));
        }
        if (!paStarted) fprintf(stderr, "audio: unavailable, playing video-only (degrade ladder step 2)\n");
    } else {
        fprintf(stderr, "audio: no audio stream found in %s, video-only\n", path);
        if (afmt) avformat_close_input(&afmt);
    }

    PlayShared ps;
    ps.wake = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    AVHWDeviceContext* hctx = (AVHWDeviceContext*)hwdev->data;
    ps.d3dlock = (AVD3D11VADeviceContext*)hctx->hwctx;
    d.hwLock = ps.d3dlock; // seekExact flushes under the same lock
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

    g_storm = Storm();
    g_storm.active = storm;
    g_storm.phase = -1; // waits for the first painted frame

    // step 5 sync report: no human to press Space in an unattended run, so
    // auto-start playback once the first frame paints, run for the window,
    // then print the two numbers spec section 7 risk 1 asks for.
    bool     syncStarted = false;
    double   syncT0 = 0, syncNextSampleAt = 0;
    int64_t  audioBaseFrames = 0;
    int64_t  videoMaxLagFrames = 0;
    std::vector<double> driftSamples;
    int      syncWindowSec = exitAfterSec > 0 ? exitAfterSec : 60;

    int64_t lastDrawn = -1;
    ID3D11ShaderResourceView *lastY = nullptr, *lastUV = nullptr;
    IDXGIKeyedMutex* lastKm = nullptr;
    int frames60 = 0; double statT0 = nowMs(); double runT0 = nowMs();
    FILETIME ftc, fte, ftk0, ftu0, ftk1, ftu1;
    GetProcessTimes(GetCurrentProcess(), &ftc, &fte, &ftk0, &ftu0);

    MSG msg;
    bool done = false;
    while (!done) {
        // vsync gate, no lock held; also prevents the 135k-fps occluded spin
        if (rend.frameWait) WaitForSingleObject(rend.frameWait, 20);
        else Sleep(8);
        while (PeekMessageW(&msg, nullptr, 0, 0, PM_REMOVE)) {
            if (msg.message == WM_QUIT) { done = true; break; }
            TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
        if (done) break;

        if (reportSync && !syncStarted && lastDrawn >= 0) {
            syncStarted = true;
            g_playing = true;
            g_playAnchorMs = nowMs();
            g_playAnchorFrame = lastDrawn;
            audioBaseFrames = g_audioFramesConsumed.load();
            syncT0 = nowMs();
            syncNextSampleAt = syncT0 + 500;
            printf("sync: playback auto-started at frame %lld (audio=%s)\n",
                   (long long)lastDrawn, paStarted ? "on" : "OFF");
        }

        if (g_playing) {
            // Audio-master clock (spec section 2/7 risk 1): while audio is
            // running, video schedules to samples actually consumed by the
            // device callback, not QPC - video CANNOT drift from audio because
            // its schedule IS the audio clock. QPC is only the fallback when
            // audio failed to open (degrade ladder step 2).
            double el = paStarted
                ? (double)(g_audioFramesConsumed.load() - audioBaseFrames) / ad.outRate
                : (nowMs() - g_playAnchorMs) / 1000.0;
            int64_t due = g_playAnchorFrame + (int64_t)(el * av_q2d(fps));
            if (g_totalFrames > 0 && due >= g_totalFrames) { due = g_totalFrames - 1; g_playing = false; }
            if (due != ps.want.load()) setWant(ps, due);
        }

        if (reportSync && syncStarted) {
            double t = nowMs();
            if (g_playing) {
                int64_t lag = ps.want.load() - lastDrawn;
                if (lag > videoMaxLagFrames) videoMaxLagFrames = lag;
            }
            if (t >= syncNextSampleAt) {
                double wallEl = (t - syncT0) / 1000.0;
                double audioEl = paStarted
                    ? (double)(g_audioFramesConsumed.load() - audioBaseFrames) / ad.outRate
                    : wallEl;
                double d = wallEl - audioEl;
                driftSamples.push_back(d < 0 ? -d : d);
                syncNextSampleAt = t + 500;
            }
            if (t - syncT0 >= syncWindowSec * 1000.0) {
                double maxDrift = 0, sumDrift = 0;
                for (double v : driftSamples) { if (v > maxDrift) maxDrift = v; sumDrift += v; }
                double avgDrift = driftSamples.empty() ? 0 : sumDrift / driftSamples.size();
                int64_t cb = g_audioCallbacks.load(), under = g_audioUnderrunFrames.load();
                printf("sync: window=%ds audio=%s underrun_frames=%lld callbacks=%lld max_video_lag_frames=%lld\n",
                       syncWindowSec, paStarted ? "on" : "off",
                       (long long)under, (long long)cb, (long long)videoMaxLagFrames);
                printf("sync: wall-vs-audio-clock drift_ms max=%.2f avg=%.2f samples=%d (threshold 33.00) -> %s\n",
                       maxDrift, avgDrift, (int)driftSamples.size(),
                       !paStarted ? "N/A-no-audio" : (maxDrift < 33.0 && under == 0 ? "PASS" : "FAIL"));
                fflush(stdout);
                done = true;
            }
        }

        if (g_storm.active) {
            double t = nowMs();
            if (g_storm.phase >= 0 && g_storm.lastLoopT > 0) {
                double gap = t - g_storm.lastLoopT;
                if (gap > g_storm.uiMaxGap) g_storm.uiMaxGap = gap;
            }
            g_storm.lastLoopT = t;
            if (g_storm.phase < 0 && lastDrawn >= 0) { // first frame painted: start
                g_storm.phase = 0; g_storm.phaseT0 = t; g_storm.nextReqAt = t + 500;
                g_storm.lastReqFrame = lastDrawn;
                printf("storm: phase A (sequential steps)\n");
            }
            // burst loop: keep the 25 req/s cadence honest even when a slow
            // present period (GPU contention) drops the UI loop below 25 Hz
            for (int burst = 0; burst < 4 &&
                 g_storm.phase >= 0 && g_storm.phase < 3 && t >= g_storm.nextReqAt; burst++) {
                bool phaseDone =
                    (g_storm.phase == 0 && g_storm.issued >= 90) ||
                    (g_storm.phase == 1 && g_storm.issued >= 30) ||
                    (g_storm.phase == 2 && t - g_storm.phaseT0 >= 30000.0);
                if (phaseDone) {
                    // let the last request land (or time out) before switching
                    if (g_storm.lastPainted || t - g_storm.lastReqT > 2000.0) {
                        g_storm.phase++; g_storm.issued = 0;
                        g_storm.phaseT0 = t; g_storm.nextReqAt = t + 500;
                        if (g_storm.phase == 1) printf("storm: phase B (far random seeks)\n");
                        if (g_storm.phase == 2) printf("storm: phase C (25 req/s scrub storm, 30 s)\n");
                        if (g_storm.phase == 3) {
                            g_storm.stats[0].report("A_seq_steps");
                            g_storm.stats[1].report("B_far_seeks");
                            g_storm.stats[2].report("C_scrub_storm");
                            printf("storm: ui_max_loop_gap_ms=%.1f\n", g_storm.uiMaxGap);
                            fflush(stdout);
                            done = true;
                        }
                    }
                } else {
                    int64_t base = g_storm.lastReqFrame >= 0 ? g_storm.lastReqFrame : lastDrawn;
                    int64_t target = -1; double interval = 40.0;
                    switch (g_storm.phase) {
                    case 0: target = base + 1; interval = 1000.0 / 30.0; break;
                    case 1: target = (int64_t)(g_storm.rnd() % (uint32_t)g_totalFrames);
                            interval = 1000.0 / 3.0; break;
                    case 2: if (g_storm.rnd() % 10 == 0)
                                target = (int64_t)(g_storm.rnd() % (uint32_t)g_totalFrames);
                            else
                                target = base + (int64_t)(g_storm.rnd() % 121) - 60;
                            interval = 40.0; break;
                    }
                    if (target < 0) target = 0;
                    if (target >= g_totalFrames) target = g_totalFrames - 1;
                    g_storm.stats[g_storm.phase].requests++;
                    g_storm.lastReqFrame = target; g_storm.lastReqT = t;
                    g_storm.lastReqPhase = g_storm.phase; g_storm.lastPainted = false;
                    g_storm.issued++;
                    g_storm.nextReqAt += interval; // drift-free schedule, not t+interval
                    setWant(ps, target);
                }
            }
        }

        int64_t want = ps.want.load();
        RingSlot& slot = ps.ring[want % RING];
        ID3D11ShaderResourceView *dy = lastY, *duv = lastUV;
        IDXGIKeyedMutex* dkm = lastKm;
        int64_t drawn = lastDrawn;
        if (slot.frame.load(std::memory_order_acquire) == want && slot.srvY) {
            dy = slot.srvY; duv = slot.srvUV; dkm = slot.kmRnd; drawn = want;
        }
        if (dy) {
            float us = ps.codedW ? (float)d.width / ps.codedW : 1.0f;
            float vs = ps.codedH ? (float)d.height / ps.codedH : 1.0f;
            if (!rend.draw(dkm, dy, duv, us, vs)) { continue; } // slot busy: retry next loop
            if (drawn != lastDrawn) {
                lastDrawn = drawn; lastY = dy; lastUV = duv; lastKm = dkm;
                if (drawn == g_stepTarget) {
                    printf("step to %lld latency=%.2f ms\n", (long long)drawn, nowMs() - g_stepT0);
                    g_stepTarget = -1;
                }
                if (g_storm.active && !g_storm.lastPainted && drawn == g_storm.lastReqFrame) {
                    StormStats& st = g_storm.stats[g_storm.lastReqPhase];
                    st.lat.push_back(nowMs() - g_storm.lastReqT);
                    st.painted++;
                    g_storm.lastPainted = true;
                }
            }
            frames60++;
        } else {
            Sleep(2); // nothing decoded yet
        }

        if (nowMs() - statT0 > 1000.0) {
            printf("stat: drawn=%lld want=%lld fps=%.1f playing=%d seeks=%d aborts=%d dec=%d bf=%d lock=%.1f draw=%.1f pres=%.1f\n",
                   (long long)lastDrawn, (long long)want,
                   frames60 * 1000.0 / (nowMs() - statT0), g_playing ? 1 : 0,
                   ps.seeksDone.load(), ps.seeksAborted.load(),
                   ps.framesDecoded.load(), ps.backfills.load(),
                   rend.lastLockMs, rend.lastDrawMs, rend.lastPresentMs);
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
    if (paStream) { Pa_StopStream(paStream); Pa_CloseStream(paStream); }
    if (haveAudio) {
        audioQuit = true;
        audioThread.join();
        ad.close();
        g_audioRing = nullptr;
    }
    if (afmt) avformat_close_input(&afmt);
    if (paInited) Pa_Terminate();
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
    bool storm = false;
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
        else if (a == "--storm")       storm = true;
        else { fprintf(stderr, "unknown arg %s\n", a.c_str()); return 2; }
    }
    if (!mode || !file) {
        fprintf(stderr, "usage: becky-engine-probe --decode|--seek-test|--play|--play-reel <file> "
                        "[--frames N] [--fps A/B] [--targets a,b,c] [--report-sync] [--storm] [--exit-after N]\n");
        return 2;
    }
    av_log_set_level(getenv("PROBE_DEBUG") ? AV_LOG_DEBUG : AV_LOG_WARNING);

    if (!strcmp(mode, "decode")) return cmdDecode(file, frames);
    if (!strcmp(mode, "seek"))   return cmdSeekTest(file, fps, targets);
    if (!strcmp(mode, "play"))   return cmdPlay(file, exitAfter, storm, reportSync);
    fprintf(stderr, "mode %s not implemented yet (--play-reel gapless segment chain is out of scope tonight - "
                    "use --play --report-sync for single-source audio/video sync proof)\n", mode);
    return 2;
}
