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
#include <cstdio>
#include <cstdint>
#include <cstring>
#include <cstdlib>
#include <string>
#include <vector>
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
    fprintf(stderr, "mode %s not implemented yet (steps 4-5 pending)\n", mode);
    return 2;
}
