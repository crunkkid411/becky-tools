// becky-timeline - native NLE timeline for Becky Review (embedded) + standalone editor.
//
// EMBEDDED (--wid, launched by Becky Review Native): a TIMELINE-ONLY view/controller.
// The Go engine (becky-review-engine) owns the edit model and mpv owns the preview
// (bridge state - HANDOFF-NATIVE-TIMELINE.md 10.A3). This process draws the timeline at
// GPU speed (D3D11 FLIP swapchain so it composites over the WebView2), decodes REAL
// audio waveforms (sample-true min/max pyramid - NOT the engine's 200-bucket SVG peaks),
// and turns mouse gestures into semantic NDJSON events on stdout:
//   {"ev":"pointer"}                        ANY mousedown (host refocuses the WebView so
//                                           click-then-Spacebar always works - 12.3)
//   {"ev":"scrub","t":s,"final":b}          ruler/empty drag seek (page -> seekTimeline)
//   {"ev":"clipclick","t":s,"id":"..."}     clip-body click; the PAGE decides: paused=seek,
//                                           playing=move the STOCK (fb7 canon)
//   {"ev":"select","ids":["..."]}           selection changed (page mirrors it)
//   {"ev":"edit","kind":"trim","id","in","out"}       trim-handle release -> set_trim
//   {"ev":"edit","kind":"reorder","id","to"}          body-drag drop      -> reorder
//   {"ev":"edit","kind":"reorder_many","ids","to"}    multi-drag drop     -> reorder_many
//   {"ev":"view","pxPerSec":n,"scroll":s}   zoom/scroll changed (page label + proxy gating)
//   {"ev":"threshold","level":x}            threshold bar dragged (page skip logic)
//   {"ev":"quiet","ranges":[[s,e],...]}     quiet stretches from the REAL peaks (page skips them)
// State arrives as stdin ops (one JSON per line):
//   {"op":"loadreel","reel":{clips:[{id,source,in,out,label,color,ready}],sel,pxPerSec,
//                            scroll,playhead,thresholdOn,thresholdLevel,view}}
//   {"op":"seek","t":s,"quiet":true,"playing":b,"stock":s|-1,"flash":b}   app playhead + the
//                            secondary STOCK bar (pause-return bookmark; blinks while diverged)
//   {"op":"vis","on":b}                            idle when the pane is hidden
//   {"op":"zoom","f":1.5}|{"op":"zoom","pps":n,"x":px}   (buttons/keys forwarded by the page)
// WHEEL is captured natively via a WH_MOUSE_LL hook (cursor-over-pane, focus-independent):
//   plain wheel = ZOOM anchored to the playhead (this app's convention - "Jordan's ask"),
//   Ctrl+wheel  = horizontal pan.  (Matches the DOM timeline's handler exactly.)
// NO video decode happens embedded - mpv is the preview; no double-decode.
//
// WAVEFORM DECODE IS WINDOWED: only the seconds actually ON the timeline are decoded,
// by SEEKING the audio-only pipeline straight to each clip's window (instant), audio
// streams only (expose-all-streams=false - never decodes the video), at BELOW_NORMAL
// priority so mpv playback is never starved. Results merge into full-length sentinel
// arrays + a per-second coverage map, cached as BPK2.
//
// STANDALONE (no --wid): the original self-contained editor - 2 GStreamer d3d11 (NVDEC)
// decoder layers, A/B tracks + PiP composite, local keys (Space/S/Del/I/O/G), local clock.
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <d3d11.h>
#include "imgui.h"
#include "imgui_impl_win32.h"
#include "imgui_impl_dx11.h"
#include "json.hpp"

#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <cstdio>
#include <cstdint>
#include <cstring>
#include <cmath>
#include <algorithm>
#include <atomic>
#include <condition_variable>
#include <deque>
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <string>
#include <thread>
#include <vector>
#include <iostream>
#include <fstream>
using json = nlohmann::json;

static double nowSec() {
    static LARGE_INTEGER fq = [] { LARGE_INTEGER f; QueryPerformanceFrequency(&f); return f; }();
    LARGE_INTEGER c; QueryPerformanceCounter(&c);
    return (double)c.QuadPart / fq.QuadPart;
}
static void fwdslash(std::string& s) { for (auto& c : s) if (c == '\\') c = '/'; }
static std::string baseName(const std::string& p) {
    size_t i = p.find_last_of("/\\");
    return i == std::string::npos ? p : p.substr(i + 1);
}

// ---------------- GStreamer VIDEO decoder (one per layer; STANDALONE preview only) ----------------
struct Layer { GstElement* pipe = nullptr; GstBus* bus = nullptr; std::string loaded; };
static Layer g_layer[2];
static std::vector<uint8_t> g_rgba;
static int g_vw = 0, g_vh = 0;
static double g_composeMs = 0;

static bool layerInit(Layer& L, std::string file) {
    fwdslash(file);
    char s[2048];
    snprintf(s, sizeof s,
        "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! d3d11convert ! "
        "video/x-raw(memory:D3D11Memory),format=RGBA ! d3d11download ! appsink name=s sync=false max-buffers=2",
        file.c_str());
    GError* e = nullptr; L.pipe = gst_parse_launch(s, &e);
    if (!L.pipe || e) { fprintf(stderr, "parse: %s\n", e ? e->message : "?"); return false; }
    L.bus = gst_element_get_bus(L.pipe);
    gst_element_set_state(L.pipe, GST_STATE_PAUSED);
    return gst_element_get_state(L.pipe, nullptr, nullptr, 15 * GST_SECOND) == GST_STATE_CHANGE_SUCCESS;
}
static bool layerLoad(Layer& L, const std::string& src) {
    if (L.pipe && L.loaded == src) return true;
    if (L.pipe) { gst_element_set_state(L.pipe, GST_STATE_NULL); gst_object_unref(L.bus); gst_object_unref(L.pipe); L.pipe = nullptr; }
    if (!layerInit(L, src)) { L.loaded.clear(); return false; }
    L.loaded = src; return true;
}
static void layerSeek(Layer& L, double srcSec) {
    GstClockTime pos = (GstClockTime)(srcSec * GST_SECOND);
    gst_element_seek_simple(L.pipe, GST_FORMAT_TIME,
        (GstSeekFlags)(GST_SEEK_FLAG_FLUSH | GST_SEEK_FLAG_ACCURATE), (gint64)pos);
}
static bool layerPull(Layer& L, GstVideoFrame* f, GstSample** out) {
    GstMessage* m = gst_bus_timed_pop_filtered(L.bus, 5 * GST_SECOND,
        (GstMessageType)(GST_MESSAGE_ASYNC_DONE | GST_MESSAGE_ERROR));
    if (!m) return false;
    bool err = GST_MESSAGE_TYPE(m) == GST_MESSAGE_ERROR; gst_message_unref(m);
    if (err) return false;
    GstElement* sink = gst_bin_get_by_name(GST_BIN(L.pipe), "s");
    GstSample* smp = gst_app_sink_pull_preroll(GST_APP_SINK(sink)); gst_object_unref(sink);
    if (!smp) return false;
    GstVideoInfo info;
    if (!gst_video_info_from_caps(&info, gst_sample_get_caps(smp))) { gst_sample_unref(smp); return false; }
    if (!gst_video_frame_map(f, &info, gst_sample_get_buffer(smp), GST_MAP_READ)) { gst_sample_unref(smp); return false; }
    *out = smp; return true;
}

// ---------------- ACCURATE WAVEFORMS: windowed, seek-first min/max peak pyramid ----------------
// L0 = min+max int8 per 64 samples @ 48 kHz mono (750 bins/sec), L1 = per 1024, L2 = per 16384.
// ABSOLUTE scale (int16 full-scale = 1.0). Arrays are sized for the FULL source duration and
// initialized to the EMPTY sentinel (min=127 > max=-128); decode jobs fill only the windows the
// timeline actually shows, so a clip from minute 47 of a 2-hour livestream is ready in <1s.
static const int    kSpb = 64;
static const int    kPeakRate = 48000;
static const double kBinsPerSec = (double)kPeakRate / kSpb;   // 750
static std::atomic<int> g_fillEpoch{ 0 };   // bumped when decode lands (redraw + quiet recompute)

struct Peaks {
    std::mutex mx;
    std::vector<int8_t> n0, x0, n1, x1, n2, x2;
    std::vector<uint8_t> secFilled;           // 1 byte per SECOND of source decoded
    size_t bins = 0;
    double duration = 0;
    bool ready = false;                       // arrays sized; drawable
    bool failed = false;
    bool dirty = false;                       // cache needs (re)saving after jobs drain
    std::deque<std::pair<double, double>> jobs;   // [start,end) source-second windows, front = next
    std::condition_variable cv;
    double lastMissReq = 0;
    std::string source, cachePath;
};
static std::map<std::string, std::shared_ptr<Peaks>> g_peaks;
static std::mutex g_peaksMx;
static std::mutex g_decMx; static std::condition_variable g_decCv; static int g_decActive = 0;
// While the user is playing or mid-gesture, decode with ONE worker instead of two so the
// E:\ HDD + GPU are never contended during interaction (12.2c). Set each frame by the main loop.
static std::atomic<bool> g_busyHint{ false };

static uint64_t fnv1a64(const std::string& s) {
    uint64_t h = 1469598103934665603ULL;
    for (unsigned char c : s) { h ^= c; h *= 1099511628211ULL; }
    return h;
}
static std::wstring utf8ToWide(const std::string& s) {
    int n = MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, nullptr, 0);
    std::wstring w(n > 0 ? n - 1 : 0, L'\0');
    if (n > 0) MultiByteToWideChar(CP_UTF8, 0, s.c_str(), -1, &w[0], n);
    return w;
}
static bool fileMeta(const std::string& path, uint64_t& size, uint64_t& mtime) {
    WIN32_FILE_ATTRIBUTE_DATA fad;
    if (!GetFileAttributesExW(utf8ToWide(path).c_str(), GetFileExInfoStandard, &fad)) return false;
    size = ((uint64_t)fad.nFileSizeHigh << 32) | fad.nFileSizeLow;
    mtime = ((uint64_t)fad.ftLastWriteTime.dwHighDateTime << 32) | fad.ftLastWriteTime.dwLowDateTime;
    return true;
}
static std::string peaksCachePath(const std::string& source) {
    uint64_t size = 0, mtime = 0; fileMeta(source, size, mtime);
    char key[64]; snprintf(key, sizeof key, "|%llu|%llu", (unsigned long long)size, (unsigned long long)mtime);
    uint64_t h = fnv1a64(source + key);
    const char* base = getenv("LOCALAPPDATA");
    std::string dir = std::string(base ? base : ".") + "\\becky";
    CreateDirectoryA(dir.c_str(), nullptr);
    dir += "\\peaks"; CreateDirectoryA(dir.c_str(), nullptr);
    char fn[64]; snprintf(fn, sizeof fn, "\\%016llx.bpk", (unsigned long long)h);
    return dir + fn;
}

// size + sentinel-init all arrays for `duration` seconds. mx held by caller.
static void sizeArrays(Peaks& P, double duration) {
    P.duration = duration;
    P.bins = (size_t)(duration * kBinsPerSec) + 2;
    P.n0.assign(P.bins, 127); P.x0.assign(P.bins, -128);
    P.n1.assign(P.bins / 16 + 1, 127); P.x1.assign(P.bins / 16 + 1, -128);
    P.n2.assign(P.bins / 256 + 1, 127); P.x2.assign(P.bins / 256 + 1, -128);
    P.secFilled.assign((size_t)duration + 2, 0);
    P.ready = true;
}
// rebuild the L1/L2 bins covering L0 range [a,b). Sentinel-aware (all-empty stays empty). mx held.
static void pyramidRegion(Peaks& P, size_t a, size_t b) {
    if (b > P.bins) b = P.bins;
    if (b <= a) return;
    for (size_t i = a >> 4; i <= ((b - 1) >> 4) && i < P.n1.size(); i++) {
        int8_t mn = 127, mx = -128;
        size_t s0 = i << 4, s1 = std::min(P.bins, s0 + 16);
        for (size_t k = s0; k < s1; k++) { mn = std::min(mn, P.n0[k]); mx = std::max(mx, P.x0[k]); }
        P.n1[i] = mn; P.x1[i] = mx;
    }
    for (size_t i = a >> 8; i <= ((b - 1) >> 8) && i < P.n2.size(); i++) {
        int8_t mn = 127, mx = -128;
        size_t s0 = i << 4, s1 = std::min(P.n1.size(), s0 + 16);
        for (size_t k = s0; k < s1; k++) { mn = std::min(mn, P.n1[k]); mx = std::max(mx, P.x1[k]); }
        P.n2[i] = mn; P.x2[i] = mx;
    }
}
static bool loadPeaksCache(Peaks& P) {
    FILE* f = nullptr; fopen_s(&f, P.cachePath.c_str(), "rb");
    if (!f) return false;
    char magic[4]; uint32_t spb = 0, rate = 0; uint64_t count = 0; double dur = 0;
    bool ok = fread(magic, 1, 4, f) == 4
        && fread(&spb, 4, 1, f) == 1 && spb == (uint32_t)kSpb
        && fread(&rate, 4, 1, f) == 1 && rate == (uint32_t)kPeakRate
        && fread(&count, 8, 1, f) == 1 && count < (1ULL << 32)
        && fread(&dur, 8, 1, f) == 1 && dur > 0;
    bool v2 = ok && memcmp(magic, "BPK2", 4) == 0;
    bool v1 = ok && memcmp(magic, "BPK1", 4) == 0;
    if (ok && (v1 || v2)) {
        std::lock_guard<std::mutex> lk(P.mx);
        sizeArrays(P, dur);
        if (v2) {
            uint32_t secN = 0;
            ok = fread(&secN, 4, 1, f) == 1 && secN <= P.secFilled.size();
            if (ok && secN) ok = fread(P.secFilled.data(), 1, secN, f) == secN;
        } else {
            std::fill(P.secFilled.begin(), P.secFilled.end(), 1);   // BPK1 = whole file decoded
        }
        if (ok) {
            std::vector<int8_t> buf((size_t)count * 2);
            ok = count == 0 || fread(buf.data(), 1, buf.size(), f) == buf.size();
            if (ok) {
                size_t n = std::min((size_t)count, P.bins);
                for (size_t i = 0; i < n; i++) { P.n0[i] = buf[i * 2]; P.x0[i] = buf[i * 2 + 1]; }
                pyramidRegion(P, 0, n);
            }
        }
        if (!ok) { P.ready = false; P.bins = 0; }
    } else ok = false;
    fclose(f);
    return ok && P.ready;
}
static void savePeaksCache(Peaks& P) {   // mx held by caller
    FILE* f = nullptr; fopen_s(&f, P.cachePath.c_str(), "wb");
    if (!f) return;
    fwrite("BPK2", 1, 4, f);
    uint32_t spb = kSpb, rate = kPeakRate; uint64_t count = P.bins; double dur = P.duration;
    uint32_t secN = (uint32_t)P.secFilled.size();
    fwrite(&spb, 4, 1, f); fwrite(&rate, 4, 1, f); fwrite(&count, 8, 1, f); fwrite(&dur, 8, 1, f);
    fwrite(&secN, 4, 1, f);
    fwrite(P.secFilled.data(), 1, secN, f);
    std::vector<int8_t> buf(P.bins * 2);
    for (size_t i = 0; i < P.bins; i++) { buf[i * 2] = P.n0[i]; buf[i * 2 + 1] = P.x0[i]; }
    fwrite(buf.data(), 1, buf.size(), f);
    fclose(f);
    P.dirty = false;
}

// decode ONE contiguous window [a,b) source-seconds through a persistent audio-only pipeline.
// Seeks straight to `a` (audio seeks are near-instant; no GOP walk) and EOSes at `b`.
static void decodeWindow(Peaks& P, GstElement* pipe, GstElement* sink, double a, double b) {
    if (!gst_element_seek(pipe, 1.0, GST_FORMAT_TIME,
        (GstSeekFlags)(GST_SEEK_FLAG_FLUSH | GST_SEEK_FLAG_ACCURATE),
        GST_SEEK_TYPE_SET, (gint64)(a * GST_SECOND),
        GST_SEEK_TYPE_SET, (gint64)(b * GST_SECOND))) return;
    for (;;) {
        GstSample* smp = gst_app_sink_try_pull_sample(GST_APP_SINK(sink), GST_SECOND);
        if (!smp) {
            if (gst_app_sink_is_eos(GST_APP_SINK(sink))) break;
            GstBus* bus = gst_element_get_bus(pipe);
            GstMessage* m = gst_bus_pop_filtered(bus, GST_MESSAGE_ERROR);
            gst_object_unref(bus);
            if (m) { gst_message_unref(m); break; }
            continue;
        }
        GstBuffer* buf = gst_sample_get_buffer(smp);
        GstClockTime pts = GST_BUFFER_PTS(buf);
        GstMapInfo mi;
        if (GST_CLOCK_TIME_IS_VALID(pts) && gst_buffer_map(buf, &mi, GST_MAP_READ)) {
            const int16_t* sm = (const int16_t*)mi.data;
            size_t ns = mi.size / 2;
            uint64_t samplePos = (uint64_t)((double)pts / GST_SECOND * kPeakRate);
            std::lock_guard<std::mutex> lk(P.mx);
            size_t firstBin = (size_t)(samplePos / kSpb), lastBin = firstBin;
            for (size_t i = 0; i < ns; i++) {
                size_t bin = (size_t)((samplePos + i) / kSpb);
                if (bin >= P.bins) break;
                int8_t q = (int8_t)(sm[i] >> 8);
                if (q < P.n0[bin]) P.n0[bin] = q;
                if (q > P.x0[bin]) P.x0[bin] = q;
                lastBin = bin;
            }
            pyramidRegion(P, firstBin, lastBin + 1);
            gst_buffer_unmap(buf, &mi);
        }
        gst_sample_unref(smp);
    }
    {   // mark fully-covered whole seconds (partial edge seconds re-decode later; cheap)
        std::lock_guard<std::mutex> lk(P.mx);
        for (size_t s = (size_t)std::ceil(a); s + 1 <= (size_t)std::floor(b) && s < P.secFilled.size(); s++)
            P.secFilled[s] = 1;
        P.dirty = true;
    }
    g_fillEpoch.fetch_add(1);
}

static void peaksWorker(std::shared_ptr<Peaks> P) {
    SetThreadPriority(GetCurrentThread(), THREAD_PRIORITY_BELOW_NORMAL);   // never starve mpv
    if (loadPeaksCache(*P)) g_fillEpoch.fetch_add(1);
    // audio-only pipeline: caps + expose-all-streams=false means the VIDEO stream is never
    // decoded (the old full-decode fought mpv for the GPU/disk - the "won't play" complaint)
    GError* uerr = nullptr;
    char* uri = gst_filename_to_uri(P->source.c_str(), &uerr);
    if (!uri) { if (uerr) g_error_free(uerr); P->failed = true; return; }
    char desc[2600];
    snprintf(desc, sizeof desc,
        "uridecodebin uri=\"%s\" caps=\"audio/x-raw\" expose-all-streams=false ! "
        "audioconvert ! audioresample ! audio/x-raw,format=S16LE,channels=1,rate=%d ! "
        "appsink name=as sync=false",
        uri, kPeakRate);
    g_free(uri);
    GError* e = nullptr;
    GstElement* pipe = gst_parse_launch(desc, &e);
    if (!pipe || e) { if (e) g_error_free(e); P->failed = true; return; }
    GstElement* sink = gst_bin_get_by_name(GST_BIN(pipe), "as");
    // probe the duration once (PAUSED preroll reads headers + a first packet - fast)
    if (!P->ready) {
        gst_element_set_state(pipe, GST_STATE_PAUSED);
        if (gst_element_get_state(pipe, nullptr, nullptr, 20 * GST_SECOND) == GST_STATE_CHANGE_FAILURE) {
            P->failed = true;   // no audio stream / unreadable -> blank lane, never a hang
            gst_element_set_state(pipe, GST_STATE_NULL);
            gst_object_unref(sink); gst_object_unref(pipe);
            return;
        }
        gint64 d = 0;
        if (gst_element_query_duration(pipe, GST_FORMAT_TIME, &d) && d > 0) {
            std::lock_guard<std::mutex> lk(P->mx);
            sizeArrays(*P, (double)d / GST_SECOND);
        } else {
            P->failed = true;
            gst_element_set_state(pipe, GST_STATE_NULL);
            gst_object_unref(sink); gst_object_unref(pipe);
            return;
        }
    }
    gst_element_set_state(pipe, GST_STATE_PLAYING);
    g_fillEpoch.fetch_add(1);   // ready -> redraw

    std::unique_lock<std::mutex> lk(P->mx);
    for (;;) {
        if (P->jobs.empty()) {
            if (P->dirty) { savePeaksCache(*P); }          // persist once the queue drains
            P->cv.wait_for(lk, std::chrono::seconds(2));   // idle; stay warm for new jobs
            if (P->jobs.empty()) continue;
        }
        auto job = P->jobs.front(); P->jobs.pop_front();
        double a = std::max(0.0, job.first), b = std::min(P->duration, job.second);
        // split into UNFILLED runs so already-decoded seconds are never re-read
        std::vector<std::pair<double, double>> runs;
        double runA = -1;
        for (size_t s = (size_t)a; s <= (size_t)b && s < P->secFilled.size(); s++) {
            bool filled = P->secFilled[s] != 0;
            if (!filled && runA < 0) runA = std::max(a, (double)s);
            if ((filled || s == (size_t)b) && runA >= 0) { runs.push_back({ runA, std::min(b, (double)s + 1) }); runA = -1; }
        }
        if (runA >= 0) runs.push_back({ runA, b });
        lk.unlock();
        for (auto& r : runs) {
            if (r.second - r.first < 0.01) continue;
            {   // global cap: 2 sources decoding at once idle, 1 while playing/gesturing (12.2c)
                std::unique_lock<std::mutex> g(g_decMx);
                g_decCv.wait(g, [] { return g_decActive < (g_busyHint.load() ? 1 : 2); });
                g_decActive++;
            }
            decodeWindow(*P, pipe, sink, r.first, r.second);
            {
                std::lock_guard<std::mutex> g(g_decMx);
                g_decActive--;
            }
            g_decCv.notify_one();
        }
        lk.lock();
    }
}

static std::shared_ptr<Peaks> peaksGet(const std::string& source) {
    std::lock_guard<std::mutex> lk(g_peaksMx);
    auto it = g_peaks.find(source);
    return it == g_peaks.end() ? nullptr : it->second;
}
static std::shared_ptr<Peaks> peaksEnsure(const std::string& source) {
    if (source.empty()) return nullptr;
    std::lock_guard<std::mutex> lk(g_peaksMx);
    auto it = g_peaks.find(source);
    if (it != g_peaks.end()) return it->second;
    auto P = std::make_shared<Peaks>();
    P->source = source;
    P->cachePath = peaksCachePath(source);
    g_peaks[source] = P;
    std::thread(peaksWorker, P).detach();
    return P;
}
// queue a decode window (newest first - what just landed on the timeline draws first)
static void peaksRequest(const std::string& source, double a, double b) {
    auto P = peaksEnsure(source);
    if (!P || P->failed) return;
    std::lock_guard<std::mutex> lk(P->mx);
    P->jobs.push_front({ std::max(0.0, a), b });
    P->cv.notify_one();
}

// ---------------- the clip tracks ----------------
struct Clip {
    double in, out, compStart;
    std::string label, source, id;
    uint8_t r = 0, g = 174, b = 239;   // source tint (page palette); default = the old blue
    bool ready = true;                 // page-confirmed windowed proxy (false = "preparing")
};
static std::vector<Clip> g_track[2];   // 0 = A (main), 1 = B (PiP; standalone only)
static double g_compDur = 0;
static bool g_group = true;

static void relabel(int tr) {
    const char* p = tr == 0 ? "clip " : "pip ";
    for (size_t i = 0; i < g_track[tr].size(); i++)
        if (g_track[tr][i].label.empty() || g_track[tr][i].label.compare(0, 4, tr == 0 ? "clip" : "pip ") == 0)
            g_track[tr][i].label = p + std::to_string(i + 1);
}
static void packTrack(int tr) {
    double cs = 0;
    for (auto& c : g_track[tr]) { c.compStart = cs; cs += c.out - c.in; }
}
static void splitTrack(int tr, double t) {
    for (size_t i = 0; i < g_track[tr].size(); i++) {
        Clip& c = g_track[tr][i]; double d = c.out - c.in;
        if (t > c.compStart + 0.05 && t < c.compStart + d - 0.05) {
            double srcT = c.in + (t - c.compStart);
            Clip right = c;   // keep the source tint + ready state on the new half
            right.in = srcT; right.compStart = t; right.label.clear(); right.id.clear();
            c.out = srcT;
            g_track[tr].insert(g_track[tr].begin() + i + 1, right); relabel(tr); return;
        }
    }
}
static void deleteTrack(int tr, double t) {
    for (size_t i = 0; i < g_track[tr].size(); i++) {
        Clip& c = g_track[tr][i]; double d = c.out - c.in;
        if (t >= c.compStart && t < c.compStart + d) {
            g_track[tr].erase(g_track[tr].begin() + i);
            for (size_t j = i; j < g_track[tr].size(); j++) g_track[tr][j].compStart -= d;
            relabel(tr); return;
        }
    }
}
static void recomputeDur() {
    g_compDur = 0;
    for (int tr = 0; tr < 2; tr++)
        if (!g_track[tr].empty()) {
            auto& c = g_track[tr].back();
            g_compDur = std::max(g_compDur, c.compStart + (c.out - c.in));
        }
}

// ---------------- STANDALONE composite (video preview; skipped when embedded) ----------------
static void compose(double t) {
    LARGE_INTEGER fq, a, b; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&a);
    GstVideoFrame fa, fb; GstSample* sa = nullptr; GstSample* sb = nullptr;
    Clip* ca = nullptr; Clip* cb = nullptr;
    for (auto& c : g_track[0]) { double d = c.out - c.in; if (t >= c.compStart && t < c.compStart + d) { ca = &c; break; } }
    for (auto& c : g_track[1]) { double d = c.out - c.in; if (t >= c.compStart && t < c.compStart + d) { cb = &c; break; } }
    if (!ca && !g_track[0].empty()) ca = &g_track[0].back();
    if (!cb && !g_track[1].empty()) cb = &g_track[1].back();
    if (!ca) return;
    if (!layerLoad(g_layer[0], ca->source)) return;
    bool haveB = cb && layerLoad(g_layer[1], cb->source);
    double ta = ca->in + (t > ca->compStart ? t - ca->compStart : 0); if (ta > ca->out) ta = ca->out;
    layerSeek(g_layer[0], ta);
    if (haveB) { double tb = cb->in + (t > cb->compStart ? t - cb->compStart : 0); if (tb > cb->out) tb = cb->out; layerSeek(g_layer[1], tb); }
    if (!layerPull(g_layer[0], &fa, &sa)) return;
    if (haveB && !layerPull(g_layer[1], &fb, &sb)) haveB = false;
    int wa = GST_VIDEO_FRAME_WIDTH(&fa), ha = GST_VIDEO_FRAME_HEIGHT(&fa);
    uint8_t* da = (uint8_t*)GST_VIDEO_FRAME_PLANE_DATA(&fa, 0); int sta = GST_VIDEO_FRAME_PLANE_STRIDE(&fa, 0);
    g_vw = wa; g_vh = ha; g_rgba.resize((size_t)wa * ha * 4);
    for (int y = 0; y < ha; y++) memcpy(&g_rgba[(size_t)y * wa * 4], da + (size_t)y * sta, (size_t)wa * 4);
    if (haveB) {
        int wb = GST_VIDEO_FRAME_WIDTH(&fb), hb = GST_VIDEO_FRAME_HEIGHT(&fb);
        uint8_t* db = (uint8_t*)GST_VIDEO_FRAME_PLANE_DATA(&fb, 0); int stb = GST_VIDEO_FRAME_PLANE_STRIDE(&fb, 0);
        int pw = wb / 2, ph = hb / 2, ox = wa - pw - wa / 20, oy = ha / 20;
        for (int y = 0; y < ph; y++) { int dy = oy + y; if (dy < 0 || dy >= ha) continue;
            for (int x = 0; x < pw; x++) { int dx = ox + x; if (dx < 0 || dx >= wa) continue;
                uint8_t* src = db + (size_t)(y * 2) * stb + (size_t)(x * 2) * 4;
                uint8_t* dst = &g_rgba[((size_t)dy * wa + dx) * 4];
                for (int c = 0; c < 3; c++) dst[c] = (uint8_t)((dst[c] + src[c]) / 2); } }
        gst_video_frame_unmap(&fb); gst_sample_unref(sb);
    }
    gst_video_frame_unmap(&fa); gst_sample_unref(sa);
    QueryPerformanceCounter(&b); g_composeMs = 1000.0 * (b.QuadPart - a.QuadPart) / fq.QuadPart;
}

// ---------------- NDJSON in/out ----------------
static std::mutex g_mx; static std::vector<json> g_pending;
static std::atomic<bool> g_stdinEof{ false };
static void stdinReader() {
    std::string line;
    while (std::getline(std::cin, line)) {
        if (line.empty()) continue;
        try { json j = json::parse(line); std::lock_guard<std::mutex> lk(g_mx); g_pending.push_back(j); } catch (...) {}
    }
    g_stdinEof = true;   // host closed the pipe (app quit or was killed) - embedded, that means exit
}
static void emitJson(const json& j) { std::cout << j.dump() << "\n"; std::cout.flush(); }
static void emitState(double curSec, bool playing) {
    json s; s["t"] = curSec; s["dur"] = g_compDur; s["playing"] = playing; s["group"] = g_group;
    for (int tr = 0; tr < 2; tr++) {
        json arr = json::array();
        for (auto& c : g_track[tr])
            arr.push_back({ {"in", c.in}, {"out", c.out}, {"start", c.compStart}, {"id", c.id}, {"source", c.source} });
        s[tr == 0 ? "trackA" : "trackB"] = arr;
    }
    emitJson(s);
}

// ---------------- view + gesture state ----------------
static HWND g_parentWid = nullptr;
static HWND g_hwnd = nullptr;
static double g_pps = 60;
static double g_scrollSec = 0;
static bool g_viewInit = false;
static bool g_visible = true;
static bool g_playingExt = false;
static double g_stockSec = -1;      // secondary STOCK bar (pause-return bookmark); -1 = none
static bool g_stockFlash = false;   // blink while auditioning ahead during playback
static double g_lastUserScroll = 0;
static std::set<std::string> g_sel;
static std::string g_selAnchor;
// playback threshold (mirrors the app's: skip quiet parts during playback; never a cut)
static bool g_thrOn = false;
static double g_thrLevel = 0.14;
static bool g_quietDirty = true;
static int g_quietEpochSeen = -1;
static std::vector<std::pair<double, double>> g_quietRanges;   // comp seconds
static double g_lastQuietEmit = 0, g_lastThrEmit = 0;

struct Gesture {
    int kind = 0;              // 0 none, 1 scrub, 2 clip-pending, 3 reorder, 4 trimL, 5 trimR, 6 scrollbar, 7 threshold
    int idx = -1;
    float pressX = 0;
    bool ctrl = false, shiftK = false;
    double gIn = 0, gOut = 0;
    std::vector<int> group;
    double grabOff = 0;
    bool dragged = false;
};
static Gesture g_gest;
static json g_pendingReel; static bool g_havePendingReel = false;
static double g_lastScrubEmit = 0, g_lastViewEmit = 0;

static void emitScrub(double t, bool final_) {
    double n = nowSec();
    if (!final_ && n - g_lastScrubEmit < 0.016) return;
    g_lastScrubEmit = n;
    emitJson({ {"ev","scrub"}, {"t", t}, {"final", final_} });
}
static bool emitView() {   // false = throttled (caller may retry next frame)
    double n = nowSec();
    if (n - g_lastViewEmit < 0.1) return false;
    g_lastViewEmit = n;
    // scroll rides along so the page can gate proxy/peaks work to what's actually visible
    emitJson({ {"ev","view"}, {"pxPerSec", g_pps}, {"scroll", g_scrollSec} });
    return true;
}
static void emitSelect() {
    json ids = json::array();
    for (auto& c : g_track[0]) if (g_sel.count(c.id)) ids.push_back(c.id);
    emitJson({ {"ev","select"}, {"ids", ids} });
}
static void emitThreshold(bool final_) {
    double n = nowSec();
    if (!final_ && n - g_lastThrEmit < 0.1) return;
    g_lastThrEmit = n;
    emitJson({ {"ev","threshold"}, {"level", g_thrLevel} });
}

// quiet stretches (comp seconds) from the REAL peaks: runs where max|amp| < level for >=0.35s.
// Undecoded bins count as LOUD (never skip what we haven't heard - same rule as the app).
static void recomputeQuiet() {
    g_quietRanges.clear();
    if (!g_thrOn) return;
    std::vector<std::pair<double, double>> raw;
    for (auto& c : g_track[0]) {
        auto pk = peaksGet(c.source);
        if (!pk) continue;
        std::lock_guard<std::mutex> lk(pk->mx);
        if (!pk->ready) continue;
        long long b0 = std::max(0LL, (long long)(c.in * kBinsPerSec));
        long long b1 = std::min((long long)pk->bins, (long long)(c.out * kBinsPerSec));
        double runA = -1;
        for (long long b = b0; b <= b1; b++) {
            bool quiet = false;
            if (b < b1) {
                int8_t mn = pk->n0[b], mx = pk->x0[b];
                if (mn <= mx) {
                    double amp = std::max(std::abs((int)mn), std::abs((int)mx)) / 127.0;
                    quiet = amp < g_thrLevel;
                }
            }
            double compT = c.compStart + (b / kBinsPerSec - c.in);
            if (quiet && runA < 0) runA = compT;
            else if (!quiet && runA >= 0) { raw.push_back({ runA, compT }); runA = -1; }
        }
    }
    std::sort(raw.begin(), raw.end());
    for (auto& r : raw) {
        if (!g_quietRanges.empty() && r.first <= g_quietRanges.back().second + 0.06)
            g_quietRanges.back().second = std::max(g_quietRanges.back().second, r.second);
        else g_quietRanges.push_back(r);
    }
    g_quietRanges.erase(std::remove_if(g_quietRanges.begin(), g_quietRanges.end(),
        [](const std::pair<double, double>& r) { return r.second - r.first < 0.35; }), g_quietRanges.end());
}
static void emitQuiet() {
    double n = nowSec();
    if (n - g_lastQuietEmit < 0.3) return;
    g_lastQuietEmit = n;
    json ranges = json::array();
    for (auto& r : g_quietRanges) ranges.push_back({ r.first, r.second });
    emitJson({ {"ev","quiet"}, {"ranges", ranges} });
}

// live reel from the host
static void loadReelLive(const json& reel, double& curSec) {
    g_track[0].clear(); g_track[1].clear();
    if (reel.contains("clips") && reel["clips"].is_array()) {
        for (auto& c : reel["clips"]) {
            double i = c.value("in", 0.0), o = c.value("out", 0.0);
            std::string src = c.value("source", std::string());
            if (o <= i || src.empty()) continue;
            std::string label = c.value("label", std::string());
            if (label.empty()) label = baseName(src);
            Clip cl; cl.in = i; cl.out = o; cl.compStart = 0;
            cl.label = label; cl.source = src; cl.id = c.value("id", std::string());
            std::string hex = c.value("color", std::string());   // "#RRGGBB" from the page palette
            if (hex.size() == 7 && hex[0] == '#') {
                long v = strtol(hex.c_str() + 1, nullptr, 16);
                cl.r = (uint8_t)((v >> 16) & 0xFF); cl.g = (uint8_t)((v >> 8) & 0xFF); cl.b = (uint8_t)(v & 0xFF);
            }
            cl.ready = c.value("ready", true);
            g_track[0].push_back(cl);
        }
    }
    packTrack(0); recomputeDur();
    g_sel.clear();
    if (reel.contains("sel") && reel["sel"].is_array())
        for (auto& s : reel["sel"]) if (s.is_string()) g_sel.insert(s.get<std::string>());
    if (reel.contains("thresholdOn") && reel["thresholdOn"].is_boolean()) g_thrOn = reel["thresholdOn"].get<bool>();
    if (reel.contains("thresholdLevel") && reel["thresholdLevel"].is_number()) g_thrLevel = reel["thresholdLevel"].get<double>();
    if (reel.value("view", false) || !g_viewInit) {
        double pps = reel.value("pxPerSec", 0.0);
        if (pps > 0.01) g_pps = std::min(2000.0, std::max(0.5, pps));
        g_scrollSec = std::max(0.0, reel.value("scroll", 0.0));
        if (reel.contains("playhead") && reel["playhead"].is_number()) curSec = std::max(0.0, reel["playhead"].get<double>());
        g_viewInit = true;
    }
    if (curSec > g_compDur) curSec = g_compDur;
    // WINDOWED waveform decode: only what's on the timeline, newest first, small margins
    // for trim headroom. This is what makes waveforms appear near-instantly.
    for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);
    g_quietDirty = true;
}

// pending zoom ops (need pane geometry; applied inside drawTimeline)
struct ZoomOp { double f = 0, pps = 0; float x = -1; };
static std::vector<ZoomOp> g_extZoom;

// one edit path for BOTH human and AI. returns true if the model changed / needs a redraw.
static bool applyOp(const std::string& op, double t, const json& j, double& curSec, bool& playing) {
    auto clampT = [&](double x) { return x < 0 ? 0 : (x > g_compDur ? g_compDur : x); };
    if (op == "loadreel") {
        if (g_gest.kind != 0) { g_pendingReel = j; g_havePendingReel = true; return false; }
        if (j.contains("reel")) loadReelLive(j["reel"], curSec);
        return true;
    }
    if (op == "seek") {
        if (j.contains("stock") && j["stock"].is_number()) g_stockSec = j["stock"].get<double>();
        if (j.contains("flash") && j["flash"].is_boolean()) g_stockFlash = j["flash"].get<bool>();
        // Mid-scrub the DRAG owns the playhead; the host's echoes (mpv lagging behind the
        // drag) would fight it and jitter the bar. Drop them until the gesture ends.
        if (g_parentWid && g_gest.kind == 1) {
            if (j.value("quiet", false)) g_playingExt = j.value("playing", g_playingExt);
            return false;
        }
        curSec = clampT(t);
        if (j.value("quiet", false)) { g_playingExt = j.value("playing", g_playingExt); return true; }
        return true;
    }
    if (op == "vis") { g_visible = j.value("on", true); return false; }
    if (op == "zoom") {
        ZoomOp z;
        if (j.contains("pps") && j["pps"].is_number()) z.pps = j["pps"].get<double>();
        if (j.contains("f") && j["f"].is_number()) z.f = j["f"].get<double>();
        if (j.contains("x") && j["x"].is_number()) z.x = (float)j["x"].get<double>();
        if (z.pps > 0 || z.f > 0) g_extZoom.push_back(z);
        return true;
    }
    if (op == "wheel") { return true; }   // legacy page-forwarded wheel (queued by the main loop)
    if (op == "play") { playing = j.value("on", !playing); return true; }
    if (op == "group") { g_group = j.value("on", !g_group); return true; }
    if (op == "split") { splitTrack(0, t); if (g_group) splitTrack(1, t); return true; }
    if (op == "delete") { deleteTrack(0, t); if (g_group) deleteTrack(1, t); recomputeDur(); curSec = clampT(curSec); return true; }
    if (op == "trim_out") { splitTrack(0, t); deleteTrack(0, t); if (g_group) { splitTrack(1, t); deleteTrack(1, t); } recomputeDur(); curSec = clampT(curSec); return true; }
    if (op == "trim_in") { splitTrack(0, t); deleteTrack(0, t - 0.02); if (g_group) { splitTrack(1, t); deleteTrack(1, t - 0.02); } recomputeDur(); curSec = clampT(curSec); return true; }
    return false;
}

// ---------------- D3D11 display ----------------
static ID3D11Device* g_dev = nullptr; static ID3D11DeviceContext* g_ctx = nullptr; static IDXGISwapChain* g_swap = nullptr;
static ID3D11RenderTargetView* g_rtv = nullptr;
static ID3D11Texture2D* g_frameTex = nullptr; static ID3D11ShaderResourceView* g_frameSrv = nullptr; static int g_texW = 0, g_texH = 0;
static int g_W = 1100, g_H = 900;
static bool g_resize = false;

static void createRTV() {
    ID3D11Texture2D* bb = nullptr;
    g_swap->GetBuffer(0, __uuidof(ID3D11Texture2D), (void**)&bb);
    if (bb) { g_dev->CreateRenderTargetView(bb, nullptr, &g_rtv); bb->Release(); }
}
static bool CreateD3D(HWND h) {
    DXGI_SWAP_CHAIN_DESC sd = {};
    sd.BufferCount = 2; sd.BufferDesc.Width = g_W; sd.BufferDesc.Height = g_H;
    sd.BufferDesc.Format = DXGI_FORMAT_R8G8B8A8_UNORM;
    sd.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT; sd.OutputWindow = h;
    sd.SampleDesc.Count = 1; sd.Windowed = TRUE;
    sd.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;   // FLIP model = DWM-composited over the WebView2
    if (FAILED(D3D11CreateDeviceAndSwapChain(nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr, 0, nullptr, 0,
        D3D11_SDK_VERSION, &sd, &g_swap, &g_dev, nullptr, &g_ctx))) return false;
    createRTV();
    return true;
}
static void resizeD3D() {
    if (!g_swap || !g_ctx) return;
    if (g_rtv) { g_rtv->Release(); g_rtv = nullptr; }
    g_swap->ResizeBuffers(0, g_W, g_H, DXGI_FORMAT_UNKNOWN, 0);
    createRTV();
}
static void uploadFrame() {
    if (g_rgba.empty() || g_vw <= 0 || g_vh <= 0) return;
    if (!g_frameTex || g_texW != g_vw || g_texH != g_vh) {
        if (g_frameSrv) { g_frameSrv->Release(); g_frameSrv = nullptr; }
        if (g_frameTex) { g_frameTex->Release(); g_frameTex = nullptr; }
        D3D11_TEXTURE2D_DESC td = {};
        td.Width = g_vw; td.Height = g_vh; td.MipLevels = 1; td.ArraySize = 1;
        td.Format = DXGI_FORMAT_R8G8B8A8_UNORM; td.SampleDesc.Count = 1;
        td.Usage = D3D11_USAGE_DYNAMIC; td.BindFlags = D3D11_BIND_SHADER_RESOURCE; td.CPUAccessFlags = D3D11_CPU_ACCESS_WRITE;
        if (FAILED(g_dev->CreateTexture2D(&td, nullptr, &g_frameTex))) return;
        g_dev->CreateShaderResourceView(g_frameTex, nullptr, &g_frameSrv);
        g_texW = g_vw; g_texH = g_vh;
    }
    D3D11_MAPPED_SUBRESOURCE ms;
    if (SUCCEEDED(g_ctx->Map(g_frameTex, 0, D3D11_MAP_WRITE_DISCARD, 0, &ms))) {
        for (int y = 0; y < g_vh; y++) memcpy((uint8_t*)ms.pData + (size_t)y * ms.RowPitch, &g_rgba[(size_t)y * g_vw * 4], (size_t)g_vw * 4);
        g_ctx->Unmap(g_frameTex, 0);
    }
}

extern IMGUI_IMPL_API LRESULT ImGui_ImplWin32_WndProcHandler(HWND, UINT, WPARAM, LPARAM);
static LRESULT WINAPI WndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    if (ImGui_ImplWin32_WndProcHandler(h, m, w, l)) return true;
    // Embedded: take mouse clicks WITHOUT taking activation/keyboard focus - the page keeps the
    // keyboard (its shortcuts + search box), we keep the mouse.
    if (m == WM_MOUSEACTIVATE && g_parentWid) return MA_NOACTIVATE;
    if (m == WM_SIZE && w != SIZE_MINIMIZED) { g_W = LOWORD(l); g_H = HIWORD(l); g_resize = true; }
    if (m == WM_DESTROY) { PostQuitMessage(0); return 0; }
    return DefWindowProcW(h, m, w, l);
}

// ---------------- wheel via WH_MOUSE_LL --------------------------------------------------
// Wheel messages route to the FOCUSED window, and focus deliberately stays with the page
// (MA_NOACTIVATE) - so a low-level hook catches the wheel whenever the cursor is over OUR
// pane, no matter who has focus. The hook dispatches on this thread's message pump, so no
// locking is needed around g_extWheel.
struct WheelEvent { float notches; bool ctrl; float x; };
static std::vector<WheelEvent> g_extWheel;
static HHOOK g_mouseHook = nullptr;
static LRESULT CALLBACK MouseLLProc(int nCode, WPARAM wParam, LPARAM lParam) {
    if (nCode == HC_ACTION && wParam == WM_MOUSEWHEEL && g_hwnd && g_visible) {
        auto* d = (MSLLHOOKSTRUCT*)lParam;
        if (WindowFromPoint(d->pt) == g_hwnd) {
            RECT wr; GetWindowRect(g_hwnd, &wr);
            short delta = (short)HIWORD(d->mouseData);
            g_extWheel.push_back({ delta / 120.0f, (GetKeyState(VK_CONTROL) & 0x8000) != 0,
                                   (float)(d->pt.x - wr.left) });
        }
    }
    return CallNextHookEx(g_mouseHook, nCode, wParam, lParam);
}

// ---------------- the timeline surface ----------------
static const ImU32 COL_BG       = IM_COL32(16, 18, 22, 255);
static const ImU32 COL_LANE     = IM_COL32(24, 27, 33, 255);
static const ImU32 COL_RULERTX  = IM_COL32(160, 166, 178, 255);
static const ImU32 COL_TICK     = IM_COL32(80, 86, 98, 255);
static const ImU32 COL_TICKMIN  = IM_COL32(52, 57, 66, 255);
static const ImU32 COL_CLIP     = IM_COL32(38, 56, 84, 255);    // standalone PiP/track-B only
static const ImU32 COL_CLIPBRD  = IM_COL32(255, 255, 255, 70);
// The DOM timeline's look, mirrored (fb2/fb4/fb7): clips are TINTED by source (page palette),
// translucent when unselected + fully opaque when selected (that IS the selection cue — no
// gold outline); the waveform is white @ 50% like .cwave path { fill: rgba(255,255,255,.5) }.
static const ImU32 COL_WAVE     = IM_COL32(255, 255, 255, 128);
static const ImU32 COL_WAVEDIM  = IM_COL32(255, 255, 255, 60);
static const ImU32 COL_PLAYHEAD = IM_COL32(0, 0, 0, 255);       // black bar (playhead.JPG design)
static const ImU32 COL_PHFLAG   = IM_COL32(255, 255, 255, 255); // its white map-pin flag head
static const ImU32 COL_PHGRIP   = IM_COL32(58, 58, 58, 255);    // the 2 grip hashmarks in the flag
static const ImU32 COL_DROPMARK = IM_COL32(255, 210, 0, 255);
static const ImU32 COL_LABEL    = IM_COL32(235, 238, 245, 235);
static const ImU32 COL_PIP      = IM_COL32(0, 160, 96, 255);
static const ImU32 COL_THRBAR   = IM_COL32(255, 120, 70, 235);
static const ImU32 COL_QUIETDIM = IM_COL32(0, 0, 0, 110);

static void fmtTime(double s, char* out, size_t n, bool subSec) {
    if (s < 0) s = 0;
    int t = (int)s, h = t / 3600, m = (t % 3600) / 60, sec = t % 60;
    if (subSec) { int d = (int)((s - t) * 10); if (h) snprintf(out, n, "%d:%02d:%02d.%d", h, m, sec, d); else snprintf(out, n, "%d:%02d.%d", m, sec, d); }
    else if (h) snprintf(out, n, "%d:%02d:%02d", h, m, sec);
    else snprintf(out, n, "%d:%02d", m, sec);
}
static double rulerStep(double pps) {
    static const double steps[] = { 0.1, 0.2, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800, 3600 };
    for (double s : steps) if (s * pps >= 70) return s;
    return 7200;
}

// draw the REAL waveform for source window [cin,cout); requests decode for any missing span.
static void drawWave(ImDrawList* dl, const std::string& source, double cin, double cout,
                     float clipX0, float wx0, float wx1, float wy0, float wy1, double pps, ImU32 col) {
    auto pk = peaksGet(source);
    if (!pk || pk->failed) return;
    bool missed = false;
    {
        std::lock_guard<std::mutex> lk(pk->mx);
        if (!pk->ready) return;
        float mid = (wy0 + wy1) * 0.5f, half = (wy1 - wy0) * 0.5f - 1.0f;
        if (half < 2) return;
        int x0 = (int)std::floor(wx0), x1 = (int)std::ceil(wx1);
        for (int x = x0; x < x1; x++) {
            double s0 = cin + (x - clipX0) / pps, s1 = s0 + 1.0 / pps;
            if (s1 <= cin) continue;
            if (s0 >= cout) break;
            s0 = std::max(s0, cin); s1 = std::min(s1, cout);
            long long b0 = (long long)(s0 * kBinsPerSec), b1 = (long long)(s1 * kBinsPerSec) + 1;
            b0 = std::max(0LL, b0); b1 = std::min((long long)pk->bins, b1);
            if (b1 <= b0) continue;
            long long span = b1 - b0;
            int mn = 127, mx = -128;
            if (span >= 4096 && !pk->n2.empty()) {
                long long c0 = b0 >> 8, c1 = std::min((long long)pk->n2.size(), (b1 >> 8) + 1);
                for (long long i = c0; i < c1; i++) { mn = std::min(mn, (int)pk->n2[i]); mx = std::max(mx, (int)pk->x2[i]); }
            } else if (span >= 256 && !pk->n1.empty()) {
                long long c0 = b0 >> 4, c1 = std::min((long long)pk->n1.size(), (b1 >> 4) + 1);
                for (long long i = c0; i < c1; i++) { mn = std::min(mn, (int)pk->n1[i]); mx = std::max(mx, (int)pk->x1[i]); }
            } else {
                for (long long i = b0; i < b1; i++) { mn = std::min(mn, (int)pk->n0[i]); mx = std::max(mx, (int)pk->x0[i]); }
            }
            if (mn > mx) { missed = true; continue; }   // undecoded span (sentinel)
            float yTop = mid - (mx / 127.0f) * half;
            float yBot = mid - (mn / 127.0f) * half;
            if (yBot - yTop < 1.0f) { yTop = mid - 0.5f; yBot = mid + 0.5f; }
            dl->AddLine(ImVec2((float)x, yTop), ImVec2((float)x, yBot), col);
        }
        if (missed && nowSec() - pk->lastMissReq < 1.0) missed = false;
        if (missed) pk->lastMissReq = nowSec();
    }
    if (missed) peaksRequest(source, cin - 1.0, cout + 5.0);   // e.g. a trim revealed new material
}

// "preparing" (12.2) = the page hasn't confirmed this clip's windowed scrub proxy yet, OR
// our own peaks decode hasn't covered its window — the clip is BLATANTLY marked not-ready
// (striped + dimmed + labelled) instead of silently lagging. Scans only the guaranteed-
// markable INTERIOR whole seconds (decodeWindow never marks a partial edge second).
static bool clipPreparing(const Clip& c) {
    if (!c.ready) return true;
    auto pk = peaksGet(c.source);
    if (!pk) return true;
    if (pk->failed) return false;                    // no audio stream: nothing to wait for
    std::lock_guard<std::mutex> lk(pk->mx);
    if (!pk->ready) return true;
    long long s0 = (long long)std::ceil(c.in), s1 = (long long)std::floor(c.out) - 1;
    for (long long s = s0; s <= s1 && s >= 0 && s < (long long)pk->secFilled.size(); s++)
        if (!pk->secFilled[(size_t)s]) return true;
    return false;
}

static double snapComp(double t, double pps, double curSec, int exclIdx, float px = 8.0f) {
    double best = t, bestD = px / pps;
    auto try_ = [&](double e) { double d = std::abs(e - t); if (d < bestD) { bestD = d; best = e; } };
    for (size_t i = 0; i < g_track[0].size(); i++) {
        if ((int)i == exclIdx) continue;
        try_(g_track[0][i].compStart);
        try_(g_track[0][i].compStart + (g_track[0][i].out - g_track[0][i].in));
    }
    try_(curSec);
    return best;
}

// The full timeline surface: ruler + lane(s) + waveforms + threshold + gestures + scrollbar.
static void drawTimeline(double& curSec, bool& playing) {
    bool embedded = g_parentWid != nullptr;
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float availW = ImGui::GetContentRegionAvail().x;
    float availH = ImGui::GetContentRegionAvail().y;
    if (availW < 16 || availH < 44) return;
    float tlX = p.x, tlW = availW;
    float rulerH = 22, sbH = 12, gap = 4;
    int lanes = embedded ? 1 : (g_track[1].empty() ? 1 : 2);
    float lanesH = availH - rulerH - sbH - gap * 2;
    float laneH = lanes == 2 ? (lanesH - gap) / 2 : lanesH;
    if (laneH < 24) laneH = 24;
    float aY = p.y + rulerH + gap;
    float bY = aY + laneH + gap;
    float bot = aY + (lanes == 2 ? laneH * 2 + gap : laneH);
    float sbY = bot + gap;

    dl->AddRectFilled(p, ImVec2(p.x + tlW, sbY + sbH), COL_BG);

    ImGui::SetCursorScreenPos(p);
    ImGui::InvisibleButton("tl", ImVec2(tlW, bot - p.y));
    bool hovered = ImGui::IsItemHovered();
    bool pressed = ImGui::IsItemActivated();
    bool active = ImGui::IsItemActive();
    bool released = ImGui::IsItemDeactivated();
    ImGuiIO& io = ImGui::GetIO();
    float mx = io.MousePos.x, my = io.MousePos.y;

    auto xToSec = [&](float x) { return std::max(0.0, g_scrollSec + (x - tlX) / g_pps); };
    auto secToX = [&](double s) { return tlX + (float)((s - g_scrollSec) * g_pps); };

    // waveform geometry (the threshold bar + zoom anchor need it up front)
    float labelH = laneH > 46 ? 17.0f : 0.0f;
    float wy0 = aY + 2 + labelH, wy1 = aY + laneH - 2;
    float waveMid = (wy0 + wy1) * 0.5f, waveHalf = (wy1 - wy0) * 0.5f - 1.0f;

    // ZOOM: THIS APP'S convention (matches the DOM timeline exactly): plain wheel = zoom
    // anchored to the PLAYHEAD (fallback: cursor); Ctrl+wheel = horizontal pan.
    auto zoomTo = [&](double newPps, float anchorX) {
        double anchor = xToSec(anchorX);
        g_pps = std::min(2000.0, std::max(0.5, newPps));
        g_scrollSec = std::max(0.0, anchor - (anchorX - tlX) / g_pps);
        emitView();
    };
    auto zoomAnchorX = [&]() -> float {
        float phx = secToX(curSec);
        if (phx >= tlX && phx <= tlX + tlW) return phx;
        return hovered ? mx : tlX + tlW / 2;
    };
    auto applyWheel = [&](float notches, bool ctrl, float atX) {
        (void)atX;
        if (ctrl) {   // Ctrl+wheel = horizontal pan (DOM: scrollLeft += delta)
            g_scrollSec = std::max(0.0, g_scrollSec + (-notches * 100.0) / g_pps);
            g_lastUserScroll = nowSec();
        } else {      // plain wheel = zoom to the playhead
            zoomTo(g_pps * std::pow(1.15, (double)notches), zoomAnchorX());
        }
    };
    // Embedded, the WH_MOUSE_LL hook is the ONE wheel source. Windows' "scroll inactive
    // windows" setting ALSO delivers WM_MOUSEWHEEL to the unfocused pane -> ImGui's
    // io.MouseWheel would DOUBLE-apply every tick (measured: 2x pans). Standalone (focused,
    // no hook installed) keeps the normal ImGui path.
    if (!g_parentWid && hovered && io.MouseWheel != 0) applyWheel(io.MouseWheel, io.KeyCtrl, mx);
    for (auto& w : g_extWheel) applyWheel(w.notches, w.ctrl, tlX + w.x);
    g_extWheel.clear();
    for (auto& z : g_extZoom) {   // page-forwarded zoom (+/- buttons, ArrowUp/Down, setZoom)
        double target = z.pps > 0 ? z.pps : g_pps * z.f;
        zoomTo(target, z.x >= 0 ? tlX + z.x : zoomAnchorX());
    }
    g_extZoom.clear();

    // Middle-click + drag = pan (fb3). Not an InvisibleButton gesture (that's left-only),
    // so track it off the raw ImGui mouse state.
    static bool s_midPan = false;
    if (hovered && ImGui::IsMouseClicked(ImGuiMouseButton_Middle)) { s_midPan = true; emitJson({ {"ev","pointer"} }); }
    if (s_midPan && ImGui::IsMouseDown(ImGuiMouseButton_Middle)) {
        if (io.MouseDelta.x != 0) { g_scrollSec = std::max(0.0, g_scrollSec - io.MouseDelta.x / g_pps); g_lastUserScroll = nowSec(); }
    } else s_midPan = false;

    bool playingNow = embedded ? g_playingExt : playing;
    double viewDur = tlW / g_pps;
    if (playingNow && g_gest.kind == 0 && nowSec() - g_lastUserScroll > 1.5) {
        if (curSec < g_scrollSec || curSec > g_scrollSec + viewDur * 0.95)
            g_scrollSec = std::max(0.0, curSec - viewDur * 0.3);
    }
    // The end of the last clip must NEVER lock navigation (fb7): allow scrolling until it
    // sits at 15% from the left, i.e. ~85% of a screen of EMPTY SPACE past the end.
    double maxScroll = std::max(0.0, g_compDur - viewDur * 0.15);
    g_scrollSec = std::min(g_scrollSec, maxScroll);

    // Playback threshold = ONE horizontal bar on a dB scale (12.1, playback-threshold.JPG):
    // lane BOTTOM = -50 dB (silences nothing), lane TOP = 0 dB (silences everything).
    // g_thrLevel stays a 0..1 AMPLITUDE on the wire (10^(dB/20)) so the page's engine-peaks
    // fallback + trim-silence keep working unchanged; only the bar's MAPPING is dB.
    const double kThrFloorDb = -50.0;
    float thrLaneTop = aY + 1, thrLaneBot = aY + laneH - 1;
    auto thrY = [&]() -> float {
        double db = g_thrLevel <= 0 ? kThrFloorDb
                                    : std::max(kThrFloorDb, std::min(0.0, 20.0 * std::log10(g_thrLevel)));
        double frac = (db - kThrFloorDb) / -kThrFloorDb;   // 0 at -50 dB (bottom) .. 1 at 0 dB (top)
        return thrLaneBot - (float)(frac * (thrLaneBot - thrLaneTop));
    };
    auto onThresholdBar = [&](float x, float y) {
        return g_thrOn && x >= tlX && x <= tlX + tlW && std::abs(y - thrY()) < 6;
    };

    auto clipHit = [&](float x, float y, int& idx, int& zone) {
        idx = -1; zone = 0;
        if (y < aY || y > aY + laneH) return false;
        for (size_t i = 0; i < g_track[0].size(); i++) {
            Clip& c = g_track[0][i];
            float x0 = secToX(c.compStart), x1 = secToX(c.compStart + (c.out - c.in));
            if (x < x0 || x > x1) continue;
            idx = (int)i;
            // 10px trim zones: at Jordan's editing speed the hand is already moving when
            // the press samples, so a 7px zone missed fast edge-grabs (they became reorders).
            float hw = std::min(10.0f, (x1 - x0) / 4);
            if ((x1 - x0) > 20 && x - x0 <= hw) zone = 4;
            else if ((x1 - x0) > 20 && x1 - x <= hw) zone = 5;
            else zone = 0;
            return true;
        }
        return false;
    };

    if (hovered && g_gest.kind == 0) {
        int hi, hz;
        if (onThresholdBar(mx, my)) ImGui::SetMouseCursor(ImGuiMouseCursor_ResizeNS);
        else if (clipHit(mx, my, hi, hz) && (hz == 4 || hz == 5)) ImGui::SetMouseCursor(ImGuiMouseCursor_ResizeEW);
    }

    // ---- gesture begin ----
    if (pressed) {
        // ANY press: tell the host so it hands keyboard focus back to the page — this is
        // what makes "click, then Spacebar" (Jordan's most-used command) work instantly (12.3).
        emitJson({ {"ev","pointer"} });
        int idx, zone;
        g_gest = Gesture{};
        g_gest.pressX = mx; g_gest.ctrl = io.KeyCtrl; g_gest.shiftK = io.KeyShift;
        if (onThresholdBar(mx, my)) {
            g_gest.kind = 7;   // drag the threshold level
        } else if (clipHit(mx, my, idx, zone)) {
            g_gest.idx = idx;
            Clip& c = g_track[0][idx];
            if (zone == 4) { g_gest.kind = 4; g_gest.gIn = c.in; g_gest.gOut = c.out; }
            else if (zone == 5) { g_gest.kind = 5; g_gest.gIn = c.in; g_gest.gOut = c.out; }
            else {
                g_gest.kind = 2;
                if (g_sel.count(c.id) && g_sel.size() > 1)
                    for (size_t i = 0; i < g_track[0].size(); i++)
                        if (g_sel.count(g_track[0][i].id)) g_gest.group.push_back((int)i);
            }
        } else {
            g_gest.kind = 1;
            curSec = std::min(xToSec(mx), g_compDur);
            playing = false;
            g_gest.gIn = curSec;
            emitScrub(curSec, false);
        }
    }

    // ---- gesture continue ----
    if (active && g_gest.kind != 0) {
        if (g_gest.kind == 1) {
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) { g_gest.gIn = curSec; emitScrub(curSec, false); }
        } else if (g_gest.kind == 7) {
            // Drag the ONE bar on the dB lane: bottom = -50 dB -> amplitude 0 (skip NOTHING),
            // top = 0 dB -> amplitude 1 (skip everything). Stored as amplitude (12.1).
            float y = std::max(thrLaneTop, std::min(thrLaneBot, my));
            double frac = (thrLaneBot - y) / std::max(1.0f, thrLaneBot - thrLaneTop);
            g_thrLevel = frac <= 0.002 ? 0.0 : std::pow(10.0, (kThrFloorDb + frac * -kThrFloorDb) / 20.0);
            g_quietDirty = true;
            emitThreshold(false);
        } else if (g_gest.kind == 2 && std::abs(mx - g_gest.pressX) > 4) {
            g_gest.kind = 3; g_gest.dragged = true;
            if (g_gest.group.empty()) g_gest.group.push_back(g_gest.idx);
        } else if (g_gest.kind == 4 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nIn = c.in + (edgeComp - c.compStart);
            nIn = std::max(0.0, std::min(nIn, c.out - 0.05));
            g_gest.gIn = nIn; g_gest.gOut = c.out;
        } else if (g_gest.kind == 5 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nOut = c.in + (edgeComp - c.compStart);
            auto pk = peaksGet(c.source);
            double srcDur = (pk && pk->ready) ? pk->duration : 0;
            if (srcDur > 0.1) nOut = std::min(nOut, srcDur);
            nOut = std::max(nOut, c.in + 0.05);
            g_gest.gIn = c.in; g_gest.gOut = nOut;
        }
    }

    // ---- gesture end ----
    if (released && g_gest.kind != 0 && g_gest.kind != 6) {
        Gesture g = g_gest; g_gest = Gesture{};
        if (g.kind == 1) {
            emitScrub(curSec, true);
        } else if (g.kind == 7) {
            emitThreshold(true);
            g_quietDirty = true;
        } else if (g.kind == 2 && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (g.ctrl) {
                if (g_sel.count(c.id)) g_sel.erase(c.id); else { g_sel.insert(c.id); g_selAnchor = c.id; }
                emitSelect();
            } else if (g.shiftK && !g_selAnchor.empty()) {
                int ai = -1, bi = g.idx;
                for (size_t i = 0; i < g_track[0].size(); i++)
                    if (g_track[0][i].id == g_selAnchor) { ai = (int)i; break; }
                if (ai >= 0) {
                    g_sel.clear();
                    for (int i = std::min(ai, bi); i <= std::max(ai, bi); i++) g_sel.insert(g_track[0][i].id);
                } else { g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id; }
                emitSelect();
            } else {
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
                double t = std::max(0.0, std::min(xToSec(mx), g_compDur));
                if (g_parentWid) {
                    // The PAGE owns the click's meaning (fb7): paused = seek here; playing =
                    // move the secondary STOCK here and never interrupt playback. Only move
                    // our own playhead when paused (the page won't seek while playing).
                    if (!g_playingExt) curSec = t;
                    emitJson({ {"ev","clipclick"}, {"t", t}, {"id", c.id} });
                } else {
                    curSec = t;
                    playing = false;
                    emitScrub(curSec, true);
                }
            }
        } else if (g.kind == 3 && !g.group.empty()) {
            double cur = xToSec(mx);
            std::set<int> dragged(g.group.begin(), g.group.end());
            int to = 0;
            for (size_t i = 0; i < g_track[0].size(); i++) {
                if (dragged.count((int)i)) continue;
                Clip& c = g_track[0][i];
                if (c.compStart + (c.out - c.in) / 2 < cur) to++;
            }
            std::vector<Clip> moved, rest;
            for (size_t i = 0; i < g_track[0].size(); i++)
                (dragged.count((int)i) ? moved : rest).push_back(g_track[0][i]);
            int ins = std::min(to, (int)rest.size());
            rest.insert(rest.begin() + ins, moved.begin(), moved.end());
            // Dropped back where it started -> NO edit: emitting a no-op reorder would push
            // a junk entry onto the engine's undo stack (Ctrl+Z would then "do nothing" once).
            bool changed = false;
            for (size_t i = 0; i < rest.size(); i++)
                if (rest[i].id != g_track[0][i].id) { changed = true; break; }
            if (changed) {
                g_track[0] = rest; packTrack(0); recomputeDur();
                if (g.group.size() > 1) {
                    json ids = json::array();
                    for (auto& c : moved) ids.push_back(c.id);
                    emitJson({ {"ev","edit"}, {"kind","reorder_many"}, {"ids", ids}, {"to", to} });
                } else {
                    emitJson({ {"ev","edit"}, {"kind","reorder"}, {"id", moved[0].id}, {"to", to} });
                }
            }
            g_quietDirty = true;
        } else if ((g.kind == 4 || g.kind == 5) && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (std::abs(g.gIn - c.in) > 0.001 || std::abs(g.gOut - c.out) > 0.001) {
                c.in = g.gIn; c.out = g.gOut;
                packTrack(0); recomputeDur();
                if (curSec > g_compDur) curSec = g_compDur;
                g_quietDirty = true;
                emitJson({ {"ev","edit"}, {"kind","trim"}, {"id", c.id}, {"in", c.in}, {"out", c.out} });
            } else {
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
            }
        }
        if (g_havePendingReel) {
            g_havePendingReel = false;
            if (g_pendingReel.contains("reel")) loadReelLive(g_pendingReel["reel"], curSec);
        }
    }

    // quiet stretches recompute (level change / edits / decode progress)
    int epoch = g_fillEpoch.load();
    if (g_thrOn && (g_quietDirty || epoch != g_quietEpochSeen)) {
        g_quietDirty = false; g_quietEpochSeen = epoch;
        recomputeQuiet();
        emitQuiet();
    }

    ImGui::PushClipRect(p, ImVec2(p.x + tlW, sbY + sbH), true);

    // ---- ruler ----
    double step = rulerStep(g_pps);
    double t0 = std::floor(g_scrollSec / step) * step;
    for (double s = t0; s <= g_scrollSec + viewDur + step; s += step) {
        float x = secToX(s);
        if (x < tlX - 60 || x > tlX + tlW + 60) continue;
        dl->AddLine(ImVec2(x, p.y + 6), ImVec2(x, p.y + rulerH), COL_TICK);
        char b[24]; fmtTime(s, b, sizeof b, step < 1.0);
        dl->AddText(ImVec2(x + 3, p.y + 3), COL_RULERTX, b);
        for (int m = 1; m < 5; m++) {
            float xm = secToX(s + step * m / 5.0);
            dl->AddLine(ImVec2(xm, p.y + rulerH - 5), ImVec2(xm, p.y + rulerH), COL_TICKMIN);
        }
    }

    // ---- lanes ----
    dl->AddRectFilled(ImVec2(tlX, aY), ImVec2(tlX + tlW, aY + laneH), COL_LANE, 3);
    if (lanes == 2) dl->AddRectFilled(ImVec2(tlX, bY), ImVec2(tlX + tlW, bY + laneH), COL_LANE, 3);

    if (g_track[0].empty()) {
        const char* msg = embedded ? "timeline empty - double-click a quote to add clips" : "timeline empty";
        ImVec2 ts = ImGui::CalcTextSize(msg);
        dl->AddText(ImVec2(tlX + (tlW - ts.x) / 2, aY + (laneH - ts.y) / 2), IM_COL32(120, 128, 140, 255), msg);
    }

    // track A clips (with the real waveforms)
    for (size_t i = 0; i < g_track[0].size(); i++) {
        Clip& c = g_track[0][i];
        double cin = c.in, cout = c.out, compStart = c.compStart;
        bool ghost = (g_gest.kind == 4 || g_gest.kind == 5) && (int)i == g_gest.idx;
        if (ghost) { cin = g_gest.gIn; cout = g_gest.gOut; }
        double drawStart = compStart, drawDur = cout - cin;
        if (ghost && g_gest.kind == 4) drawStart = compStart + (cin - c.in);
        float x0 = secToX(drawStart), x1 = secToX(drawStart + drawDur);
        if (x1 < tlX - 4 || x0 > tlX + tlW + 4) continue;
        bool selected = g_sel.count(c.id) != 0;
        bool inDrag = g_gest.kind == 3 && std::find(g_gest.group.begin(), g_gest.group.end(), (int)i) != g_gest.group.end();
        // Source tint (fb7 palette, fb2 selection): unselected = translucent, selected =
        // FULLY OPAQUE (that is the selection cue); border + handles in the SOURCE colour
        // (white edge when selected, matching the DOM's clipBorder) — never gold/green.
        ImU32 fill = IM_COL32(c.r, c.g, c.b, selected ? 232 : 62);
        if (inDrag) fill = (fill & 0x00FFFFFF) | 0x60000000;
        dl->AddRectFilled(ImVec2(x0 + 1, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), fill, 3);
        float vx0 = std::max(x0 + 1, tlX), vx1 = std::min(x1 - 1, tlX + tlW);
        if (vx1 > vx0 && wy1 - wy0 > 6)
            drawWave(dl, c.source, cin, cout, x0, vx0, vx1, wy0, wy1, g_pps,
                     inDrag ? COL_WAVEDIM : (selected ? IM_COL32(255, 255, 255, 190) : COL_WAVE));
        ImU32 brd = selected ? IM_COL32(255, 255, 255, 255) : IM_COL32(c.r, c.g, c.b, 242);
        dl->AddRect(ImVec2(x0 + 1, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), brd, 3, 0, selected ? 2.0f : 1.0f);
        if (embedded && clipPreparing(c)) {
            // BLATANTLY OBVIOUS not-ready state (12.2): dim + diagonal stripes + label,
            // clearing the moment the proxy + peaks are in.
            ImVec2 pr0(std::max(x0 + 1, tlX), aY + 1), pr1(std::min(x1 - 1, tlX + tlW), aY + laneH - 1);
            if (pr1.x > pr0.x) {
                dl->PushClipRect(pr0, pr1, true);
                dl->AddRectFilled(pr0, pr1, IM_COL32(0, 0, 0, 96));
                for (float sx = x0 - laneH; sx < x1; sx += 16.0f)
                    dl->AddLine(ImVec2(sx, aY + laneH), ImVec2(sx + laneH, aY), IM_COL32(255, 255, 255, 30), 3.0f);
                const char* pmsg = "preparing...";
                ImVec2 ts = ImGui::CalcTextSize(pmsg);
                if (pr1.x - pr0.x > ts.x + 10) {
                    float cx = (pr0.x + pr1.x - ts.x) * 0.5f, cy = aY + (laneH - ts.y) * 0.5f;
                    dl->AddText(ImVec2(cx + 1, cy + 1), IM_COL32(0, 0, 0, 220), pmsg);
                    dl->AddText(ImVec2(cx, cy), IM_COL32(255, 255, 255, 240), pmsg);
                }
                dl->PopClipRect();
            }
        }
        if (labelH > 0 && x1 - x0 > 34) {
            char lab[160]; double d = cout - cin; char tb[24]; fmtTime(d, tb, sizeof tb, d < 10);
            snprintf(lab, sizeof lab, "%s  %s", c.label.c_str(), tb);
            dl->PushClipRect(ImVec2(x0 + 4, aY), ImVec2(x1 - 4, aY + labelH + 4), true);
            dl->AddText(ImVec2(x0 + 6, aY + 4), IM_COL32(0, 0, 0, 200), lab);   // shadow: readable on bright tints
            dl->AddText(ImVec2(x0 + 5, aY + 3), COL_LABEL, lab);
            dl->PopClipRect();
        }
        if (x1 - x0 > 20) {
            ImU32 hcol = IM_COL32(c.r, c.g, c.b, selected ? 255 : 150);   // handles match the source colour (fb4)
            dl->AddRectFilled(ImVec2(x0 + 1, aY + 1), ImVec2(x0 + 4, aY + laneH - 1), hcol);
            dl->AddRectFilled(ImVec2(x1 - 4, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), hcol);
        }
    }

    // ---- playback threshold: dim the quiet stretches + ONE draggable dB bar (12.1) ----
    if (g_thrOn) {
        for (auto& r : g_quietRanges) {
            float qx0 = secToX(r.first), qx1 = secToX(r.second);
            if (qx1 < tlX || qx0 > tlX + tlW) continue;
            dl->AddRectFilled(ImVec2(std::max(qx0, tlX), aY + 1), ImVec2(std::min(qx1, tlX + tlW), aY + laneH - 1), COL_QUIETDIM);
        }
        float ty = thrY();
        dl->AddLine(ImVec2(tlX, ty), ImVec2(tlX + tlW, ty), COL_THRBAR, 2.0f);
        dl->AddRectFilled(ImVec2(tlX + 10, ty - 4), ImVec2(tlX + 20, ty + 4), COL_THRBAR, 2.0f);   // grab knob
        char tb[64];
        if (g_thrLevel <= 0) snprintf(tb, sizeof tb, "threshold -50 dB - skipping nothing (drag up)");
        else snprintf(tb, sizeof tb, "threshold %.0f dB  (drag)", std::max(kThrFloorDb, 20.0 * std::log10(g_thrLevel)));
        float labY = (ty - thrLaneTop > 20) ? ty - 18 : ty + 6;   // label above, or below near the top
        dl->AddText(ImVec2(tlX + 26, labY), COL_THRBAR, tb);
    }

    // track B (standalone PiP lane, view-only)
    if (lanes == 2) {
        for (auto& c : g_track[1]) {
            float x0 = secToX(c.compStart), x1 = secToX(c.compStart + (c.out - c.in));
            if (x1 < tlX || x0 > tlX + tlW) continue;
            dl->AddRectFilled(ImVec2(x0 + 1, bY + 1), ImVec2(x1 - 1, bY + laneH - 1), COL_PIP, 3);
            dl->AddRect(ImVec2(x0 + 1, bY + 1), ImVec2(x1 - 1, bY + laneH - 1), COL_CLIPBRD, 3);
            if (x1 - x0 > 34) dl->AddText(ImVec2(x0 + 5, bY + 3), COL_LABEL, c.label.c_str());
        }
    }

    // ---- reorder dropmark ----
    if (g_gest.kind == 3 && !g_gest.group.empty()) {
        double cur = xToSec(mx);
        std::set<int> dragged(g_gest.group.begin(), g_gest.group.end());
        int to = 0;
        for (size_t i = 0; i < g_track[0].size(); i++) {
            if (dragged.count((int)i)) continue;
            Clip& c = g_track[0][i];
            if (c.compStart + (c.out - c.in) / 2 < cur) to++;
        }
        float markX = tlX; bool found = false; float lastRight = tlX; int seen = 0;
        for (size_t i = 0; i < g_track[0].size(); i++) {
            if (dragged.count((int)i)) continue;
            Clip& c = g_track[0][i];
            float x0 = secToX(c.compStart), x1 = secToX(c.compStart + (c.out - c.in));
            if (seen == to) { markX = x0; found = true; break; }
            lastRight = x1; seen++;
        }
        if (!found) markX = lastRight;
        dl->AddLine(ImVec2(markX, aY - 2), ImVec2(markX, aY + laneH + 2), COL_DROPMARK, 2.5f);
    }

    // ---- the secondary STOCK bar (fb7 item 1/2): where Pause snaps back to. A plain
    // black bar (no flag head), blinking black<->white while auditioning ahead. Drawn
    // UNDER the playhead so the two read as one when coincident. ----
    if (embedded && g_stockSec >= 0) {
        float sx = secToX(g_stockSec);
        if (sx >= tlX - 2 && sx <= tlX + tlW + 2) {
            bool wht = g_stockFlash && std::fmod(nowSec(), 0.8) >= 0.4;
            dl->AddLine(ImVec2(sx, p.y + 4), ImVec2(sx, bot), wht ? IM_COL32(255, 255, 255, 255) : COL_PLAYHEAD, 2.0f);
        }
    }

    // ---- playhead: BLACK bar + white map-pin flag head with 2 grip hashmarks
    // (playhead.JPG / the DOM's #playhead design — not the old gold). ----
    float px = secToX(curSec);
    if (px >= tlX - 2 && px <= tlX + tlW + 2) {
        dl->AddLine(ImVec2(px, p.y + 2), ImVec2(px, bot), COL_PLAYHEAD, 2.0f);
        float fw = 8, ftop = p.y + 1, fmid = p.y + 13, ftip = p.y + 20;
        dl->AddRectFilled(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), COL_PHFLAG);
        dl->AddTriangleFilled(ImVec2(px - fw, fmid), ImVec2(px + fw, fmid), ImVec2(px, ftip), COL_PHFLAG);
        dl->AddRect(ImVec2(px - fw, ftop), ImVec2(px + fw, fmid), IM_COL32(0, 0, 0, 115));
        dl->AddLine(ImVec2(px - 2.5f, ftop + 2), ImVec2(px - 2.5f, fmid - 2), COL_PHGRIP, 2.0f);
        dl->AddLine(ImVec2(px + 2.5f, ftop + 2), ImVec2(px + 2.5f, fmid - 2), COL_PHGRIP, 2.0f);
    }

    ImGui::PopClipRect();

    // ---- scrollbar (range matches the overscroll clamp: dur + ~85% of a view of empty) ----
    ImGui::SetCursorScreenPos(ImVec2(tlX, sbY));
    ImGui::InvisibleButton("tlsb", ImVec2(tlW, sbH));
    double total = std::max(viewDur, maxScroll + viewDur);
    float thW = total > 0 ? (float)(viewDur / total) * tlW : tlW;
    thW = std::max(thW, 24.0f);
    float thX = total > viewDur ? tlX + (float)(g_scrollSec / (total - viewDur)) * (tlW - thW) : tlX;
    dl->AddRectFilled(ImVec2(tlX, sbY), ImVec2(tlX + tlW, sbY + sbH), IM_COL32(28, 31, 37, 255), 4);
    dl->AddRectFilled(ImVec2(thX, sbY + 1), ImVec2(thX + thW, sbY + sbH - 1), IM_COL32(95, 104, 120, 255), 4);
    if (ImGui::IsItemActivated()) {
        emitJson({ {"ev","pointer"} });   // scrollbar grabs restore the page's keyboard too (12.3)
        g_gest = Gesture{}; g_gest.kind = 6;
        g_gest.grabOff = (mx >= thX && mx <= thX + thW) ? (mx - thX) : thW / 2;
    }
    if (ImGui::IsItemActive() && g_gest.kind == 6 && total > viewDur && tlW > thW) {
        double frac = (mx - g_gest.grabOff - tlX) / (tlW - thW);
        g_scrollSec = std::max(0.0, std::min(1.0, frac)) * (total - viewDur);
        g_lastUserScroll = nowSec();
    }
    if (ImGui::IsItemDeactivated() && g_gest.kind == 6) {
        g_gest = Gesture{};
        if (g_havePendingReel) {
            g_havePendingReel = false;
            double cs = curSec;
            if (g_pendingReel.contains("reel")) loadReelLive(g_pendingReel["reel"], cs);
            curSec = cs;
        }
    }

    // View changed (zoom emits eagerly; scroll from pans/scrollbar/auto-follow lands here):
    // tell the page so it can gate proxy/peaks work to what's actually on screen. Only
    // remember the state when the (throttled) emit actually went out.
    static double s_lastPps = -1, s_lastScroll = -1;
    if (std::abs(g_pps - s_lastPps) > 1e-9 || std::abs(g_scrollSec - s_lastScroll) > 0.05) {
        if (emitView()) { s_lastPps = g_pps; s_lastScroll = g_scrollSec; }
    }
}

int main(int argc, char** argv) {
    gst_init(&argc, &argv);
    std::vector<std::string> a;
    for (int i = 1; i < argc; i++) {
        std::string s = argv[i];
        if (s == "--wid" && i + 1 < argc) g_parentWid = (HWND)(intptr_t)_strtoi64(argv[++i], nullptr, 10);
        else a.push_back(s);
    }
    bool embedded = g_parentWid != nullptr;
    if (a.empty() && !embedded) {
        fprintf(stderr, "usage: becky-timeline [--wid H] [<proxyA> <proxyB> | --reel reel.json]\n");
        return 2;
    }

    double curSec = 0;
    if (!a.empty() && a[0] == "--reel") {
        if (a.size() < 2) { fprintf(stderr, "need a reel path\n"); return 2; }
        std::ifstream in(a[1]); json r;
        if (!in) { fprintf(stderr, "cannot open reel %s\n", a[1].c_str()); return 2; }
        try { in >> r; } catch (...) { fprintf(stderr, "bad reel json\n"); return 2; }
        std::string sa = r.value("sourceA", std::string()), sb = r.value("sourceB", std::string());
        double cs = 0;
        for (auto& c : r["trackA"]) { double i = c.at("in"), o = c.at("out"); g_track[0].push_back({ i, o, cs, "", c.value("source", sa), c.value("id", std::string()) }); cs += o - i; }
        if (r.contains("trackB") && !r["trackB"].empty()) { double bs = 0; for (auto& c : r["trackB"]) { double i = c.at("in"), o = c.at("out"); g_track[1].push_back({ i, o, bs, "", c.value("source", sb), "" }); bs += o - i; } }
        else if (!sb.empty()) g_track[1].push_back({ 0, cs, 0, "", sb, "" });
    } else if (!a.empty()) {
        if (a.size() < 2) { fprintf(stderr, "need <proxyA> <proxyB>\n"); return 2; }
        struct Seg { double in, out; }; Seg segs[] = { {5,25}, {40,60}, {75,95}, {100,115} };
        double cs = 0; for (auto& s : segs) { g_track[0].push_back({ s.in, s.out, cs, "", a[0], "" }); cs += s.out - s.in; }
        g_track[1].push_back({ 0, cs, 0, "", a[1], "" });
    }
    recomputeDur(); relabel(0); relabel(1);
    if (!embedded && !g_track[0].empty()) {
        layerLoad(g_layer[0], g_track[0][0].source);
        if (!g_track[1].empty()) layerLoad(g_layer[1], g_track[1][0].source);
    }
    for (auto& c : g_track[0]) peaksRequest(c.source, c.in - 1.0, c.out + 5.0);

    std::thread(stdinReader).detach();

    WNDCLASSEXW wc = { sizeof wc, CS_OWNDC, WndProc, 0, 0, GetModuleHandle(nullptr), nullptr, LoadCursor(nullptr, IDC_ARROW), nullptr, nullptr, L"beckytl", nullptr };
    RegisterClassExW(&wc);
    HWND hwnd;
    if (embedded) {
        RECT rc = { 0,0,900,300 }; GetClientRect(g_parentWid, &rc);
        g_W = (rc.right > 1) ? rc.right : 900; g_H = (rc.bottom > 1) ? rc.bottom : 300;
        hwnd = CreateWindowW(wc.lpszClassName, L"becky-timeline", WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS, 0, 0, g_W, g_H, g_parentWid, nullptr, wc.hInstance, nullptr);
    } else {
        hwnd = CreateWindowW(wc.lpszClassName, L"becky-timeline", WS_OVERLAPPEDWINDOW, 80, 40, g_W, g_H, nullptr, nullptr, wc.hInstance, nullptr);
    }
    g_hwnd = hwnd;
    if (!CreateD3D(hwnd)) { fprintf(stderr, "D3D11 init failed\n"); return 4; }
    ShowWindow(hwnd, SW_SHOW); UpdateWindow(hwnd);
    // focus-independent wheel (embedded): plain wheel zooms, Ctrl+wheel pans - see MouseLLProc
    if (embedded) g_mouseHook = SetWindowsHookExW(WH_MOUSE_LL, MouseLLProc, GetModuleHandleW(nullptr), 0);

    IMGUI_CHECKVERSION(); ImGui::CreateContext(); ImGui::GetIO().IniFilename = nullptr; ImGui::StyleColorsDark();
    ImGui::GetIO().FontGlobalScale = 1.2f;
    ImGui_ImplWin32_Init(hwnd); ImGui_ImplDX11_Init(g_dev, g_ctx);

    double lastComposed = -1; bool playing = false;
    LARGE_INTEGER fq, prev; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&prev);

    bool run = true;
    while (run) {
        MSG msg; while (PeekMessage(&msg, nullptr, 0, 0, PM_REMOVE)) { TranslateMessage(&msg); DispatchMessage(&msg); if (msg.message == WM_QUIT) run = false; }
        if (!run) break;
        LARGE_INTEGER now; QueryPerformanceCounter(&now);
        double dt = (double)(now.QuadPart - prev.QuadPart) / fq.QuadPart; prev = now;

        // ops from stdin (host + AI). Page-forwarded ops carry a STRING "t" (the message tag),
        // so every field read is type-guarded and each op is fenced: degrade, never crash.
        std::vector<json> ops; { std::lock_guard<std::mutex> lk(g_mx); ops.swap(g_pending); }
        for (auto& j : ops) {
            try {
                std::string op = j.value("op", std::string());
                double t = (j.contains("t") && j["t"].is_number()) ? j["t"].get<double>() : curSec;
                if (op == "wheel") { g_extWheel.push_back({ (float)j.value("dy", 0.0), j.value("ctrl", false), (float)j.value("x", 0.0) }); continue; }
                bool quiet = (j.contains("quiet") && j["quiet"].is_boolean() && j["quiet"].get<bool>())
                    || op == "loadreel" || op == "vis" || op == "zoom";
                if (applyOp(op, t, j, curSec, playing)) {
                    lastComposed = -1;
                    if (!quiet) emitState(curSec, playing);
                }
            } catch (...) { /* malformed op - ignore */ }
        }

        // Embedded orphan guard: if the host app died (force-killed - no clean shutdown),
        // our stdin hits EOF and/or the parent HWND vanishes. Exit instead of leaking a
        // GPU process forever.
        if (embedded && (g_stdinEof || !IsWindow(g_parentWid))) break;

        // Interacting or playing -> waveform decode drops to ONE worker (12.2c): the E:\
        // HDD + GPU belong to mpv + the gesture, not background peaks.
        g_busyHint = (embedded ? g_playingExt : playing) || g_gest.kind != 0;

        if (!g_visible) { Sleep(30); continue; }

        if (embedded) {
            RECT rc; if (GetClientRect(g_parentWid, &rc) && rc.right > 0 && rc.bottom > 0 && (rc.right != g_W || rc.bottom != g_H)) MoveWindow(hwnd, 0, 0, rc.right, rc.bottom, TRUE);
        }
        if (!embedded && GetForegroundWindow() == hwnd) {
            if (GetAsyncKeyState(VK_SPACE) & 1) { applyOp("play", curSec, json::object(), curSec, playing); }
            if (GetAsyncKeyState('S') & 1) { applyOp("split", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState(VK_DELETE) & 1) { applyOp("delete", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('O') & 1) { applyOp("trim_out", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('I') & 1) { applyOp("trim_in", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('G') & 1) { applyOp("group", curSec, json::object(), curSec, playing); }
        }
        if (playing && !embedded) { curSec += dt; if (curSec >= g_compDur) curSec = 0; }

        if (!embedded && !g_track[0].empty() && curSec != lastComposed) { compose(curSec); uploadFrame(); lastComposed = curSec; }
        if (g_resize) { resizeD3D(); g_resize = false; }

        ImGui_ImplDX11_NewFrame(); ImGui_ImplWin32_NewFrame(); ImGui::NewFrame();
        ImGui::SetNextWindowPos({ 0, 0 }); ImGui::SetNextWindowSize({ (float)g_W, (float)g_H });
        ImGui::Begin("becky", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus | ImGuiWindowFlags_NoScrollbar | ImGuiWindowFlags_NoScrollWithMouse);

        if (!embedded) {
            ImGui::Text("becky-timeline   %s   %.1fs / %.0fs   compose %.2f ms (%.0f fps)",
                playing ? "PLAYING" : "paused", curSec, g_compDur, g_composeMs, g_composeMs > 0 ? 1000.0 / g_composeMs : 0);
            ImGui::SameLine(); ImGui::TextColored(g_group ? ImVec4(1, 0.82f, 0, 1) : ImVec4(0.5f, 0.5f, 0.5f, 1), "  [G]roup %s", g_group ? "ON" : "off");
            ImGui::SameLine(); ImGui::TextDisabled("  [S]plit  trim [I]/[O]  [Del]  |  drag=scrub  wheel=zoom  Ctrl+wheel=pan  Space=play");
            if (g_vw > 0 && g_frameSrv) {
                float availW = ImGui::GetContentRegionAvail().x, availH = g_H * 0.52f;
                float sc = availH / g_vh; if (g_vw * sc > availW) sc = availW / g_vw;
                float iw = g_vw * sc, ih = g_vh * sc;
                ImGui::SetCursorPosX((availW - iw) * 0.5f + ImGui::GetCursorPosX());
                ImGui::Image((ImTextureID)g_frameSrv, { iw, ih });
                ImGui::Dummy({ 0, 4 });
            }
        }
        drawTimeline(curSec, playing);
        ImGui::End();

        ImGui::Render();
        float clr[4] = { 0.06f, 0.07f, 0.09f, 1.0f };
        g_ctx->OMSetRenderTargets(1, &g_rtv, nullptr);
        g_ctx->ClearRenderTargetView(g_rtv, clr);
        ImGui_ImplDX11_RenderDrawData(ImGui::GetDrawData());
        g_swap->Present(1, 0);
    }

    if (g_mouseHook) UnhookWindowsHookEx(g_mouseHook);
    ImGui_ImplDX11_Shutdown(); ImGui_ImplWin32_Shutdown(); ImGui::DestroyContext();
    if (g_frameSrv) g_frameSrv->Release(); if (g_frameTex) g_frameTex->Release();
    if (g_rtv) g_rtv->Release(); if (g_swap) g_swap->Release(); if (g_ctx) g_ctx->Release(); if (g_dev) g_dev->Release();
    for (auto& L : g_layer) { if (L.pipe) { gst_element_set_state(L.pipe, GST_STATE_NULL); gst_object_unref(L.bus); gst_object_unref(L.pipe); } }
    return 0;
}
