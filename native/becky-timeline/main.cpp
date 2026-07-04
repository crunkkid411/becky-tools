// becky-timeline - native NLE timeline for Becky Review (embedded) + standalone editor.
//
// EMBEDDED (--wid, launched by Becky Review Native): a TIMELINE-ONLY view/controller.
// The Go engine (becky-review-engine) owns the edit model and mpv owns the preview
// (bridge state - HANDOFF-NATIVE-TIMELINE.md 10.A3). This process draws the timeline at
// GPU speed (D3D11 FLIP swapchain so it composites over the WebView2), decodes REAL
// audio waveforms (sample-true min/max pyramid - NOT the engine's 200-bucket SVG peaks),
// and turns mouse gestures into semantic NDJSON events on stdout:
//   {"ev":"scrub","t":s,"final":b}          drag/click seek (page -> seekTimeline)
//   {"ev":"select","ids":["..."]}           selection changed (page mirrors it)
//   {"ev":"edit","kind":"trim","id","in","out"}       trim-handle release -> set_trim
//   {"ev":"edit","kind":"reorder","id","to"}          body-drag drop      -> reorder
//   {"ev":"edit","kind":"reorder_many","ids","to"}    multi-drag drop     -> reorder_many
//   {"ev":"view","pxPerSec":n}              zoom changed (page zoom label)
// State arrives as stdin ops (one JSON per line):
//   {"op":"loadreel","reel":{clips:[{id,source,in,out,label}],sel,pxPerSec,scroll,playhead,view}}
//   {"op":"seek","t":s,"quiet":true,"playing":b}   follow the app playhead (no echo)
//   {"op":"vis","on":b}                            idle when the pane is hidden
//   {"op":"zoom","f":1.5} {"op":"wheel","dy":n,"ctrl":b,"x":px}
// NO video decode happens embedded - mpv is the preview; no double-decode.
//
// STANDALONE (no --wid): the original self-contained editor - 2 GStreamer d3d11 (NVDEC)
// decoder layers, A/B tracks + PiP composite, local keys (Space/S/Del/I/O/G), local clock.
//
// HUMAN and AI drive the SAME surface: ops in NDJSON on stdin, state/events on stdout.
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
static void layerSeek(Layer& L, double srcSec) {   // fire the seek; DON'T wait (layers decode in parallel)
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

// ---------------- ACCURATE WAVEFORMS: per-source min/max peak pyramid --------------------
// The REAL audio samples, decoded once per source in a background thread:
//   L0 = min+max int8 per 64 samples @ 48 kHz mono  (750 bins/sec - sample-true at any zoom
//        the timeline can reach), L1 = per 1024, L2 = per 16384 (for zoomed-out draws).
// ABSOLUTE scale (int16 full-scale = 1.0), never per-clip normalized - silence looks silent
// and levels are comparable across clips (the engine's SVG peaks were neither).
// Cached at %LOCALAPPDATA%\becky\peaks\<fnv1a64(path|size|mtime)>.bpk so a source decodes once ever.
static const int    kSpb = 64;                 // samples per L0 bin
static const int    kPeakRate = 48000;
static const double kBinsPerSec = (double)kPeakRate / kSpb;   // 750

struct Peaks {
    std::mutex mx;
    std::vector<int8_t> n0, x0, n1, x1, n2, x2;   // min/max at L0, L1(x16), L2(x256)
    size_t count = 0;                             // published L0 bins
    bool done = false, failed = false;
    double duration = 0;                          // source duration (sec) once known
};
static std::map<std::string, std::shared_ptr<Peaks>> g_peaks;
static std::mutex g_peaksMx;
static std::mutex g_decMx; static std::condition_variable g_decCv; static int g_decActive = 0;

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
// derive L1/L2 tails from L0 (idempotent; call with mx held after appending L0 bins)
static void derivePyramid(Peaks& P) {
    while (P.n1.size() < P.n0.size() / 16) {
        size_t i = P.n1.size() * 16;
        int8_t mn = 127, mx = -128;
        for (size_t k = i; k < i + 16; k++) { mn = std::min(mn, P.n0[k]); mx = std::max(mx, P.x0[k]); }
        P.n1.push_back(mn); P.x1.push_back(mx);
    }
    while (P.n2.size() < P.n1.size() / 16) {
        size_t i = P.n2.size() * 16;
        int8_t mn = 127, mx = -128;
        for (size_t k = i; k < i + 16; k++) { mn = std::min(mn, P.n1[k]); mx = std::max(mx, P.x1[k]); }
        P.n2.push_back(mn); P.x2.push_back(mx);
    }
}
static bool loadPeaksCache(const std::string& path, Peaks& P) {
    FILE* f = nullptr; fopen_s(&f, path.c_str(), "rb");
    if (!f) return false;
    char magic[4]; uint32_t spb = 0, rate = 0; uint64_t count = 0; double dur = 0;
    bool ok = fread(magic, 1, 4, f) == 4 && memcmp(magic, "BPK1", 4) == 0
        && fread(&spb, 4, 1, f) == 1 && spb == (uint32_t)kSpb
        && fread(&rate, 4, 1, f) == 1 && rate == (uint32_t)kPeakRate
        && fread(&count, 8, 1, f) == 1 && count < (1ULL << 32)
        && fread(&dur, 8, 1, f) == 1;
    if (ok) {
        std::vector<int8_t> buf(count * 2);
        ok = count == 0 || fread(buf.data(), 1, buf.size(), f) == buf.size();
        if (ok) {
            std::lock_guard<std::mutex> lk(P.mx);
            P.n0.resize(count); P.x0.resize(count);
            for (size_t i = 0; i < count; i++) { P.n0[i] = buf[i * 2]; P.x0[i] = buf[i * 2 + 1]; }
            derivePyramid(P);
            P.count = count; P.duration = dur; P.done = true;
        }
    }
    fclose(f);
    return ok;
}
static void savePeaksCache(const std::string& path, Peaks& P) {
    FILE* f = nullptr; fopen_s(&f, path.c_str(), "wb");
    if (!f) return;
    fwrite("BPK1", 1, 4, f);
    uint32_t spb = kSpb, rate = kPeakRate; uint64_t count = P.count; double dur = P.duration;
    fwrite(&spb, 4, 1, f); fwrite(&rate, 4, 1, f); fwrite(&count, 8, 1, f); fwrite(&dur, 8, 1, f);
    std::vector<int8_t> buf(P.count * 2);
    for (size_t i = 0; i < P.count; i++) { buf[i * 2] = P.n0[i]; buf[i * 2 + 1] = P.x0[i]; }
    fwrite(buf.data(), 1, buf.size(), f);
    fclose(f);
}
static void decodePeaksThread(std::string source, std::shared_ptr<Peaks> P) {
    {   // ponytail: cap concurrent audio decodes at 2; a reel can reference many sources
        std::unique_lock<std::mutex> lk(g_decMx);
        g_decCv.wait(lk, [] { return g_decActive < 2; });
        g_decActive++;
    }
    std::string cache = peaksCachePath(source);
    if (!loadPeaksCache(cache, *P)) {
        GError* err = nullptr;
        char* uri = gst_filename_to_uri(source.c_str(), &err);
        if (!uri) { if (err) g_error_free(err); P->failed = true; }
        else {
            char desc[2600];
            snprintf(desc, sizeof desc,
                "uridecodebin uri=\"%s\" ! audioconvert ! audioresample ! "
                "audio/x-raw,format=S16LE,channels=1,rate=%d ! appsink name=as sync=false",
                uri, kPeakRate);
            g_free(uri);
            GError* e = nullptr;
            GstElement* pipe = gst_parse_launch(desc, &e);
            if (!pipe || e) { if (e) g_error_free(e); P->failed = true; }
            else {
                GstElement* sink = gst_bin_get_by_name(GST_BIN(pipe), "as");
                gst_element_set_state(pipe, GST_STATE_PLAYING);
                int32_t mn = 127 * 256, mx = -128 * 256; int cnt = 0;
                bool gotDur = false;
                double lastPub = nowSec();
                for (;;) {
                    GstSample* smp = gst_app_sink_try_pull_sample(GST_APP_SINK(sink), GST_SECOND);
                    if (!smp) {
                        if (gst_app_sink_is_eos(GST_APP_SINK(sink))) break;
                        // a source with NO audio stream errors instead of EOS-ing; without this
                        // check the thread would block forever and pin a decode slot
                        GstBus* bus = gst_element_get_bus(pipe);
                        GstMessage* m = gst_bus_pop_filtered(bus, GST_MESSAGE_ERROR);
                        gst_object_unref(bus);
                        if (m) { gst_message_unref(m); break; }
                        continue;   // just slow (network drive) - keep waiting
                    }
                    if (!gotDur) {
                        gint64 d = 0;
                        if (gst_element_query_duration(pipe, GST_FORMAT_TIME, &d) && d > 0) {
                            P->duration = (double)d / GST_SECOND;
                            std::lock_guard<std::mutex> lk(P->mx);
                            size_t want = (size_t)(P->duration * kBinsPerSec) + 1024;
                            P->n0.reserve(want); P->x0.reserve(want);
                            gotDur = true;
                        }
                    }
                    GstBuffer* buf = gst_sample_get_buffer(smp);
                    GstMapInfo mi;
                    if (gst_buffer_map(buf, &mi, GST_MAP_READ)) {
                        const int16_t* sm = (const int16_t*)mi.data;
                        size_t ns = mi.size / 2;
                        std::lock_guard<std::mutex> lk(P->mx);
                        for (size_t i = 0; i < ns; i++) {
                            int32_t v = sm[i];
                            if (v < mn) mn = v;
                            if (v > mx) mx = v;
                            if (++cnt == kSpb) {
                                P->n0.push_back((int8_t)(mn >> 8)); P->x0.push_back((int8_t)(mx >> 8));
                                mn = 127 * 256; mx = -128 * 256; cnt = 0;
                            }
                        }
                        derivePyramid(*P);
                        gst_buffer_unmap(buf, &mi);
                    }
                    gst_sample_unref(smp);
                    if (nowSec() - lastPub > 0.05) {   // progressive publish while decoding
                        std::lock_guard<std::mutex> lk(P->mx);
                        P->count = P->n0.size();
                        lastPub = nowSec();
                    }
                }
                {
                    std::lock_guard<std::mutex> lk(P->mx);
                    P->count = P->n0.size();
                    if (P->duration <= 0) P->duration = P->count / kBinsPerSec;
                    P->done = P->count > 0;
                    P->failed = P->count == 0;
                    if (P->done) savePeaksCache(cache, *P);
                }
                gst_element_set_state(pipe, GST_STATE_NULL);
                gst_object_unref(sink); gst_object_unref(pipe);
            }
        }
    }
    {
        std::lock_guard<std::mutex> lk(g_decMx);
        g_decActive--;
    }
    g_decCv.notify_one();
}
static std::shared_ptr<Peaks> peaksFor(const std::string& source) {
    if (source.empty()) return nullptr;
    std::lock_guard<std::mutex> lk(g_peaksMx);
    auto it = g_peaks.find(source);
    if (it != g_peaks.end()) return it->second;
    auto P = std::make_shared<Peaks>();
    g_peaks[source] = P;
    std::thread(decodePeaksThread, source, P).detach();
    return P;
}

// ---------------- the clip tracks ----------------
struct Clip { double in, out, compStart; std::string label; std::string source; std::string id; };
static std::vector<Clip> g_track[2];   // 0 = A (main), 1 = B (PiP; standalone only)
static double g_compDur = 0;
static bool g_group = true;            // grouped -> split/delete/trim hit BOTH tracks (standalone A/B)

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
            Clip right{ srcT, c.out, t, "", c.source, "" }; c.out = srcT;
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
static void stdinReader() {
    std::string line;
    while (std::getline(std::cin, line)) {
        if (line.empty()) continue;
        try { json j = json::parse(line); std::lock_guard<std::mutex> lk(g_mx); g_pending.push_back(j); } catch (...) {}
    }
}
static void emitJson(const json& j) { std::cout << j.dump() << "\n"; std::cout.flush(); }
static void emitState(double curSec, bool playing) {   // becky's AI reads this to see the result
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
static HWND g_parentWid = nullptr;     // --wid: embedded child of BRN's timeline pane
static double g_pps = 60;              // px per second (zoom)
static double g_scrollSec = 0;
static bool g_viewInit = false;        // first loadreel with view:true adopts pxPerSec/scroll
static bool g_visible = true;          // {"op":"vis"} - skip render/Present while hidden
static bool g_playingExt = false;      // embedded: play state reported by the app
static double g_lastUserScroll = 0;    // suppress auto-follow briefly after a manual scroll
static std::set<std::string> g_sel;
static std::string g_selAnchor;

struct Gesture {
    int kind = 0;              // 0 none, 1 scrub, 2 clip-pending, 3 reorder, 4 trimL, 5 trimR, 6 scrollbar
    int idx = -1;              // clip index (track 0)
    float pressX = 0;
    bool ctrl = false, shiftK = false;
    double gIn = 0, gOut = 0;  // live trim ghost (source seconds)
    std::vector<int> group;    // reorder: all dragged clip indices (multi-select drag)
    double grabOff = 0;        // scrollbar: grab offset inside the thumb (px)
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
static void emitView() {
    double n = nowSec();
    if (n - g_lastViewEmit < 0.1) return;
    g_lastViewEmit = n;
    emitJson({ {"ev","view"}, {"pxPerSec", g_pps} });
}
static void emitSelect() {
    json ids = json::array();
    for (auto& c : g_track[0]) if (g_sel.count(c.id)) ids.push_back(c.id);   // timeline order
    emitJson({ {"ev","select"}, {"ids", ids} });
}

// live reel from the host: {"clips":[{id,source,in,out,label}], sel, pxPerSec, scroll, playhead, view}
static void loadReelLive(const json& reel, double& curSec) {
    g_track[0].clear(); g_track[1].clear();
    if (reel.contains("clips") && reel["clips"].is_array()) {
        for (auto& c : reel["clips"]) {
            double i = c.value("in", 0.0), o = c.value("out", 0.0);
            std::string src = c.value("source", std::string());
            if (o <= i || src.empty()) continue;
            std::string label = c.value("label", std::string());
            if (label.empty()) label = baseName(src);
            g_track[0].push_back({ i, o, 0, label, src, c.value("id", std::string()) });
        }
    }
    packTrack(0); recomputeDur();
    g_sel.clear();
    if (reel.contains("sel") && reel["sel"].is_array())
        for (auto& s : reel["sel"]) if (s.is_string()) g_sel.insert(s.get<std::string>());
    // the FIRST push after toggle-on carries view:true -> adopt the DOM view so the toggle
    // is seamless; later pushes must NOT stomp the user's local zoom/scroll
    if (reel.value("view", false) || !g_viewInit) {
        double pps = reel.value("pxPerSec", 0.0);
        if (pps > 0.01) g_pps = std::min(2000.0, std::max(0.5, pps));
        g_scrollSec = std::max(0.0, reel.value("scroll", 0.0));
        if (reel.contains("playhead")) curSec = std::max(0.0, reel.value("playhead", 0.0));
        g_viewInit = true;
    }
    if (curSec > g_compDur) curSec = g_compDur;
    for (auto& c : g_track[0]) peaksFor(c.source);   // kick waveform decode for every source
}

// one edit path for BOTH human and AI. returns true if the model changed / needs a redraw.
static bool applyOp(const std::string& op, double t, const json& j, double& curSec, bool& playing) {
    auto clampT = [&](double x) { return x < 0 ? 0 : (x > g_compDur ? g_compDur : x); };
    if (op == "loadreel") {
        if (g_gest.kind != 0) { g_pendingReel = j; g_havePendingReel = true; return false; }  // never stomp a live gesture
        if (j.contains("reel")) loadReelLive(j["reel"], curSec);
        return true;
    }
    if (op == "seek") {
        curSec = clampT(t);
        if (j.value("quiet", false)) { g_playingExt = j.value("playing", g_playingExt); return true; }
        return true;
    }
    if (op == "vis") { g_visible = j.value("on", true); return false; }
    if (op == "zoom") {
        double f = j.value("f", 1.0);
        if (f > 0) { g_pps = std::min(2000.0, std::max(0.5, g_pps * f)); emitView(); }
        return true;
    }
    if (op == "wheel") { return true; }   // queued into g_extWheel by the main loop (needs pane geometry)
    if (op == "play") { playing = j.value("on", !playing); return true; }
    if (op == "group") { g_group = j.value("on", !g_group); return true; }
    if (op == "split") { splitTrack(0, t); if (g_group) splitTrack(1, t); return true; }
    if (op == "delete") { deleteTrack(0, t); if (g_group) deleteTrack(1, t); recomputeDur(); curSec = clampT(curSec); return true; }
    if (op == "trim_out") { splitTrack(0, t); deleteTrack(0, t); if (g_group) { splitTrack(1, t); deleteTrack(1, t); } recomputeDur(); curSec = clampT(curSec); return true; }
    if (op == "trim_in") { splitTrack(0, t); deleteTrack(0, t - 0.02); if (g_group) { splitTrack(1, t); deleteTrack(1, t - 0.02); } recomputeDur(); curSec = clampT(curSec); return true; }
    return false;
}

// ---------------- D3D11 display (FLIP swapchain -> DWM composites the child over the WebView2) ----------------
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
    sd.SwapEffect = DXGI_SWAP_EFFECT_FLIP_DISCARD;   // FLIP model = DWM-composited over the WebView2 (BitBlt wasn't)
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
    // Embedded: take mouse clicks WITHOUT taking activation/keyboard focus — the page keeps the
    // keyboard (its shortcuts + search box), we keep the mouse. Without this, one click on the
    // timeline silently killed every page hotkey until the user clicked the page again.
    if (m == WM_MOUSEACTIVATE && g_parentWid) return MA_NOACTIVATE;
    if (m == WM_SIZE && w != SIZE_MINIMIZED) { g_W = LOWORD(l); g_H = HIWORD(l); g_resize = true; }
    if (m == WM_DESTROY) { PostQuitMessage(0); return 0; }
    return DefWindowProcW(h, m, w, l);
}

// ---------------- the timeline surface ----------------
static const ImU32 COL_BG       = IM_COL32(16, 18, 22, 255);
static const ImU32 COL_LANE     = IM_COL32(24, 27, 33, 255);
static const ImU32 COL_RULERTX  = IM_COL32(160, 166, 178, 255);
static const ImU32 COL_TICK     = IM_COL32(80, 86, 98, 255);
static const ImU32 COL_TICKMIN  = IM_COL32(52, 57, 66, 255);
static const ImU32 COL_CLIP     = IM_COL32(38, 56, 84, 255);
static const ImU32 COL_CLIPSEL  = IM_COL32(58, 84, 126, 255);
static const ImU32 COL_CLIPBRD  = IM_COL32(255, 255, 255, 70);
static const ImU32 COL_SELBRD   = IM_COL32(255, 210, 0, 255);
static const ImU32 COL_WAVE     = IM_COL32(66, 224, 255, 225);
static const ImU32 COL_WAVEDIM  = IM_COL32(66, 224, 255, 90);
static const ImU32 COL_PLAYHEAD = IM_COL32(255, 210, 0, 255);
static const ImU32 COL_DROPMARK = IM_COL32(255, 210, 0, 255);
static const ImU32 COL_LABEL    = IM_COL32(235, 238, 245, 235);
static const ImU32 COL_PIP      = IM_COL32(0, 160, 96, 255);

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

// draw the REAL waveform for source window [cin,cout) inside rect [wx0,wx1]x[wy0,wy1];
// clipX0 = the pixel of source-time `cin` (the clip's drawn left edge).
static void drawWave(ImDrawList* dl, const std::string& source, double cin, double cout,
                     float clipX0, float wx0, float wx1, float wy0, float wy1, double pps, ImU32 col) {
    auto pk = peaksFor(source);
    if (!pk || pk->failed) return;
    std::lock_guard<std::mutex> lk(pk->mx);
    if (pk->count == 0) return;
    float mid = (wy0 + wy1) * 0.5f, half = (wy1 - wy0) * 0.5f - 1.0f;
    if (half < 2) return;
    int x0 = (int)std::floor(wx0), x1 = (int)std::ceil(wx1);
    for (int x = x0; x < x1; x++) {
        double s0 = cin + (x - clipX0) / pps, s1 = s0 + 1.0 / pps;
        if (s1 <= cin) continue;
        if (s0 >= cout) break;
        s0 = std::max(s0, cin); s1 = std::min(s1, cout);
        long long b0 = (long long)(s0 * kBinsPerSec), b1 = (long long)(s1 * kBinsPerSec) + 1;
        b0 = std::max(0LL, b0); b1 = std::min((long long)pk->count, b1);
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
        if (mn > mx) continue;
        float yTop = mid - (mx / 127.0f) * half;
        float yBot = mid - (mn / 127.0f) * half;
        if (yBot - yTop < 1.0f) { yTop = mid - 0.5f; yBot = mid + 0.5f; }
        dl->AddLine(ImVec2((float)x, yTop), ImVec2((float)x, yBot), col);
    }
}

// snap a candidate comp-time to clip edges / the playhead within `px` pixels; excl = clip to skip
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

struct WheelEvent { float dy; bool ctrl; float x; };
static std::vector<WheelEvent> g_extWheel;   // wheel forwarded from the page (embedded)

// The full timeline surface: ruler + lane(s) + waveforms + gestures + scrollbar.
// Mutates curSec/playing; emits NDJSON events for the host when gestures commit.
static void drawTimeline(double& curSec, bool& playing) {
    bool embedded = g_parentWid != nullptr;
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float availW = ImGui::GetContentRegionAvail().x;
    float availH = ImGui::GetContentRegionAvail().y;
    if (availW < 16 || availH < 44) return;   // pane too small to interact with (mid-resize)
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

    // ---- interaction region (everything but the scrollbar row) ----
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

    // wheel: local (standalone hover) + forwarded from the page (embedded)
    auto applyWheel = [&](float notches, bool ctrl, float atX) {
        if (ctrl) {
            double f = std::pow(1.15, (double)notches);
            double anchor = xToSec(atX);
            g_pps = std::min(2000.0, std::max(0.5, g_pps * f));
            g_scrollSec = std::max(0.0, anchor - (atX - tlX) / g_pps);
            emitView();
        } else {
            g_scrollSec = std::max(0.0, g_scrollSec - notches * 80.0 / g_pps);
            g_lastUserScroll = nowSec();
        }
    };
    if (hovered && io.MouseWheel != 0) applyWheel(io.MouseWheel, io.KeyCtrl, mx);
    for (auto& w : g_extWheel) applyWheel(w.dy, w.ctrl, tlX + w.x);
    g_extWheel.clear();

    // auto-follow the playhead while the app is playing (unless the user just scrolled)
    bool playingNow = embedded ? g_playingExt : playing;
    double viewDur = tlW / g_pps;
    if (playingNow && g_gest.kind == 0 && nowSec() - g_lastUserScroll > 1.5) {
        if (curSec < g_scrollSec || curSec > g_scrollSec + viewDur * 0.95)
            g_scrollSec = std::max(0.0, curSec - viewDur * 0.3);
    }
    double maxScroll = std::max(0.0, g_compDur - viewDur * 0.5);
    g_scrollSec = std::min(g_scrollSec, maxScroll);

    // ---- hit-testing (track 0 only; the PiP lane is view-only) ----
    auto clipHit = [&](float x, float y, int& idx, int& zone) {   // zone: 0 body, 4 L handle, 5 R handle
        idx = -1; zone = 0;
        if (y < aY || y > aY + laneH) return false;
        for (size_t i = 0; i < g_track[0].size(); i++) {
            Clip& c = g_track[0][i];
            float x0 = secToX(c.compStart), x1 = secToX(c.compStart + (c.out - c.in));
            if (x < x0 || x > x1) continue;
            idx = (int)i;
            float hw = std::min(7.0f, (x1 - x0) / 4);
            if ((x1 - x0) > 20 && x - x0 <= hw) zone = 4;
            else if ((x1 - x0) > 20 && x1 - x <= hw) zone = 5;
            else zone = 0;
            return true;
        }
        return false;
    };

    // hover cursor for trim handles
    if (hovered && g_gest.kind == 0) {
        int hi, hz;
        if (clipHit(mx, my, hi, hz) && (hz == 4 || hz == 5))
            ImGui::SetMouseCursor(ImGuiMouseCursor_ResizeEW);
    }

    // ---- gesture begin ----
    if (pressed) {
        int idx, zone;
        g_gest = Gesture{};
        g_gest.pressX = mx; g_gest.ctrl = io.KeyCtrl; g_gest.shiftK = io.KeyShift;
        if (clipHit(mx, my, idx, zone)) {
            g_gest.idx = idx;
            Clip& c = g_track[0][idx];
            if (zone == 4) { g_gest.kind = 4; g_gest.gIn = c.in; g_gest.gOut = c.out; }
            else if (zone == 5) { g_gest.kind = 5; g_gest.gIn = c.in; g_gest.gOut = c.out; }
            else {
                g_gest.kind = 2;   // pending: click = seek+select, drag = reorder
                if (g_sel.count(c.id) && g_sel.size() > 1)
                    for (size_t i = 0; i < g_track[0].size(); i++)
                        if (g_sel.count(g_track[0][i].id)) g_gest.group.push_back((int)i);
            }
        } else {
            g_gest.kind = 1;   // ruler / empty lane = scrub
            curSec = std::min(xToSec(mx), g_compDur);
            playing = false;
            g_gest.gIn = curSec;   // last emitted scrub value (emit only on change)
            emitScrub(curSec, false);
        }
    }

    // ---- gesture continue ----
    if (active && g_gest.kind != 0) {
        if (g_gest.kind == 1) {
            curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
            if (std::abs(curSec - g_gest.gIn) > 1e-9) { g_gest.gIn = curSec; emitScrub(curSec, false); }
        } else if (g_gest.kind == 2 && std::abs(mx - g_gest.pressX) > 4) {
            g_gest.kind = 3; g_gest.dragged = true;
            if (g_gest.group.empty()) g_gest.group.push_back(g_gest.idx);
        } else if (g_gest.kind == 4 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nIn = c.in + (edgeComp - c.compStart);           // dragged LEFT edge follows the hand
            nIn = std::max(0.0, std::min(nIn, c.out - 0.05));
            g_gest.gIn = nIn; g_gest.gOut = c.out;
        } else if (g_gest.kind == 5 && g_gest.idx >= 0 && g_gest.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g_gest.idx];
            double edgeComp = snapComp(xToSec(mx), g_pps, curSec, g_gest.idx);
            double nOut = c.in + (edgeComp - c.compStart);
            auto pk = peaksFor(c.source);
            double srcDur = (pk && pk->done) ? pk->duration : 0;    // known source length bounds the extend
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
        } else if (g.kind == 2 && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (g.ctrl) {                       // toggle in multi-selection; playhead stays
                if (g_sel.count(c.id)) g_sel.erase(c.id); else { g_sel.insert(c.id); g_selAnchor = c.id; }
                emitSelect();
            } else if (g.shiftK && !g_selAnchor.empty()) {   // range from the anchor
                int ai = -1, bi = g.idx;
                for (size_t i = 0; i < g_track[0].size(); i++)
                    if (g_track[0][i].id == g_selAnchor) { ai = (int)i; break; }
                if (ai >= 0) {
                    g_sel.clear();
                    for (int i = std::min(ai, bi); i <= std::max(ai, bi); i++) g_sel.insert(g_track[0][i].id);
                } else { g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id; }
                emitSelect();
            } else {                            // plain click: select + seek (paused navigation)
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
                curSec = std::max(0.0, std::min(xToSec(mx), g_compDur));
                playing = false;
                emitScrub(curSec, true);
            }
        } else if (g.kind == 3 && !g.group.empty()) {
            // drop: `to` = how many NON-dragged clips sit left of the cursor (engine App.Reorder contract)
            double cur = xToSec(mx);
            std::set<int> dragged(g.group.begin(), g.group.end());
            int to = 0;
            for (size_t i = 0; i < g_track[0].size(); i++) {
                if (dragged.count((int)i)) continue;
                Clip& c = g_track[0][i];
                if (c.compStart + (c.out - c.in) / 2 < cur) to++;
            }
            // optimistic local move (the engine reply reconciles via loadreel)
            std::vector<Clip> moved, rest;
            for (size_t i = 0; i < g_track[0].size(); i++)
                (dragged.count((int)i) ? moved : rest).push_back(g_track[0][i]);
            int ins = std::min(to, (int)rest.size());
            rest.insert(rest.begin() + ins, moved.begin(), moved.end());
            g_track[0] = rest; packTrack(0); recomputeDur();
            if (g.group.size() > 1) {
                json ids = json::array();
                for (auto& c : moved) ids.push_back(c.id);
                emitJson({ {"ev","edit"}, {"kind","reorder_many"}, {"ids", ids}, {"to", to} });
            } else {
                emitJson({ {"ev","edit"}, {"kind","reorder"}, {"id", moved[0].id}, {"to", to} });
            }
        } else if ((g.kind == 4 || g.kind == 5) && g.idx >= 0 && g.idx < (int)g_track[0].size()) {
            Clip& c = g_track[0][g.idx];
            if (std::abs(g.gIn - c.in) > 0.001 || std::abs(g.gOut - c.out) > 0.001) {
                c.in = g.gIn; c.out = g.gOut;                    // optimistic; engine reconciles
                packTrack(0); recomputeDur();
                if (curSec > g_compDur) curSec = g_compDur;
                emitJson({ {"ev","edit"}, {"kind","trim"}, {"id", c.id}, {"in", c.in}, {"out", c.out} });
            } else {                                             // a click on a handle selects
                g_sel.clear(); g_sel.insert(c.id); g_selAnchor = c.id;
                emitSelect();
            }
        }
        if (g_havePendingReel) {   // a reel arrived mid-gesture: apply it now
            g_havePendingReel = false;
            if (g_pendingReel.contains("reel")) loadReelLive(g_pendingReel["reel"], curSec);
        }
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
        for (int m = 1; m < 5; m++) {   // minor ticks
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
    float labelH = laneH > 46 ? 17.0f : 0.0f;
    for (size_t i = 0; i < g_track[0].size(); i++) {
        Clip& c = g_track[0][i];
        double cin = c.in, cout = c.out, compStart = c.compStart;
        bool ghost = (g_gest.kind == 4 || g_gest.kind == 5) && (int)i == g_gest.idx;
        if (ghost) { cin = g_gest.gIn; cout = g_gest.gOut; }
        // ghost geometry: an L-trim moves the LEFT edge with the hand (the right edge + later
        // clips hold still; the ripple closes the gap on release - Vegas ripple-trim feel).
        double drawStart = compStart, drawDur = cout - cin;
        if (ghost && g_gest.kind == 4) drawStart = compStart + (cin - c.in);
        float x0 = secToX(drawStart), x1 = secToX(drawStart + drawDur);
        if (x1 < tlX - 4 || x0 > tlX + tlW + 4) continue;   // viewport gate
        bool selected = g_sel.count(c.id) != 0;
        bool inDrag = g_gest.kind == 3 && std::find(g_gest.group.begin(), g_gest.group.end(), (int)i) != g_gest.group.end();
        ImU32 fill = selected ? COL_CLIPSEL : COL_CLIP;
        if (inDrag) fill = (fill & 0x00FFFFFF) | 0x60000000;
        dl->AddRectFilled(ImVec2(x0 + 1, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), fill, 3);
        // the REAL waveform (fills the clip below the label strip)
        float wy0 = aY + 2 + labelH, wy1 = aY + laneH - 2;
        float vx0 = std::max(x0 + 1, tlX), vx1 = std::min(x1 - 1, tlX + tlW);
        if (vx1 > vx0 && wy1 - wy0 > 6)
            drawWave(dl, c.source, cin, cout, x0, vx0, vx1, wy0, wy1, g_pps, inDrag ? COL_WAVEDIM : COL_WAVE);
        dl->AddRect(ImVec2(x0 + 1, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), selected ? COL_SELBRD : COL_CLIPBRD, 3, 0, selected ? 2.0f : 1.0f);
        if (labelH > 0 && x1 - x0 > 34) {
            char lab[160]; double d = cout - cin; char tb[24]; fmtTime(d, tb, sizeof tb, d < 10);
            snprintf(lab, sizeof lab, "%s  %s", c.label.c_str(), tb);
            dl->PushClipRect(ImVec2(x0 + 4, aY), ImVec2(x1 - 4, aY + labelH + 4), true);
            dl->AddText(ImVec2(x0 + 5, aY + 3), COL_LABEL, lab);
            dl->PopClipRect();
        }
        // trim handle affordances (visible when the clip is wide enough)
        if (x1 - x0 > 20) {
            dl->AddRectFilled(ImVec2(x0 + 1, aY + 1), ImVec2(x0 + 4, aY + laneH - 1), IM_COL32(255, 255, 255, selected ? 90 : 45));
            dl->AddRectFilled(ImVec2(x1 - 4, aY + 1), ImVec2(x1 - 1, aY + laneH - 1), IM_COL32(255, 255, 255, selected ? 90 : 45));
        }
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

    // ---- playhead (gold, with a flag head) ----
    float px = secToX(curSec);
    if (px >= tlX - 2 && px <= tlX + tlW + 2) {
        dl->AddLine(ImVec2(px, p.y + 4), ImVec2(px, bot), COL_PLAYHEAD, 2.0f);
        dl->AddTriangleFilled(ImVec2(px - 6, p.y + 4), ImVec2(px + 6, p.y + 4), ImVec2(px, p.y + 13), COL_PLAYHEAD);
    }

    ImGui::PopClipRect();

    // ---- scrollbar ----
    ImGui::SetCursorScreenPos(ImVec2(tlX, sbY));
    ImGui::InvisibleButton("tlsb", ImVec2(tlW, sbH));
    double total = std::max(g_compDur, viewDur);
    float thW = total > 0 ? (float)(viewDur / total) * tlW : tlW;
    thW = std::max(thW, 24.0f);
    float thX = total > viewDur ? tlX + (float)(g_scrollSec / (total - viewDur)) * (tlW - thW) : tlX;
    dl->AddRectFilled(ImVec2(tlX, sbY), ImVec2(tlX + tlW, sbY + sbH), IM_COL32(28, 31, 37, 255), 4);
    dl->AddRectFilled(ImVec2(thX, sbY + 1), ImVec2(thX + thW, sbY + sbH - 1), IM_COL32(95, 104, 120, 255), 4);
    if (ImGui::IsItemActivated()) {
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
        if (g_havePendingReel) {   // a reel deferred during the scrollbar drag: apply it now
            g_havePendingReel = false;
            double cs = curSec;
            if (g_pendingReel.contains("reel")) loadReelLive(g_pendingReel["reel"], cs);
            curSec = cs;
        }
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
        // reel.json: { "sourceA","sourceB", "trackA":[{"in","out","source"?}], "trackB":[...] }
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
    // embedded with no reel arg: start EMPTY and wait for {"op":"loadreel"} pushes
    recomputeDur(); relabel(0); relabel(1);
    if (!embedded && !g_track[0].empty()) {
        layerLoad(g_layer[0], g_track[0][0].source);        // pre-warm -> instant first frame
        if (!g_track[1].empty()) layerLoad(g_layer[1], g_track[1][0].source);
    }
    for (auto& c : g_track[0]) peaksFor(c.source);          // waveforms for every source

    std::thread(stdinReader).detach();   // AI + host ops on stdin

    WNDCLASSEXW wc = { sizeof wc, CS_OWNDC, WndProc, 0, 0, GetModuleHandle(nullptr), nullptr, LoadCursor(nullptr, IDC_ARROW), nullptr, nullptr, L"beckytl", nullptr };
    RegisterClassExW(&wc);
    HWND hwnd;
    if (embedded) {   // a child window filling BRN's timeline pane (composites like mpv)
        RECT rc = { 0,0,900,300 }; GetClientRect(g_parentWid, &rc);
        g_W = (rc.right > 1) ? rc.right : 900; g_H = (rc.bottom > 1) ? rc.bottom : 300;
        hwnd = CreateWindowW(wc.lpszClassName, L"becky-timeline", WS_CHILD | WS_VISIBLE | WS_CLIPSIBLINGS, 0, 0, g_W, g_H, g_parentWid, nullptr, wc.hInstance, nullptr);
    } else {
        hwnd = CreateWindowW(wc.lpszClassName, L"becky-timeline", WS_OVERLAPPEDWINDOW, 80, 40, g_W, g_H, nullptr, nullptr, wc.hInstance, nullptr);
    }
    if (!CreateD3D(hwnd)) { fprintf(stderr, "D3D11 init failed\n"); return 4; }
    ShowWindow(hwnd, SW_SHOW); UpdateWindow(hwnd);

    IMGUI_CHECKVERSION(); ImGui::CreateContext(); ImGui::GetIO().IniFilename = nullptr; ImGui::StyleColorsDark();
    ImGui::GetIO().FontGlobalScale = 1.2f;   // Jordan reads the screen himself - keep text big
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
                    if (!quiet) emitState(curSec, playing);   // AI contract: state after every (non-follow) op
                }
            } catch (...) { /* malformed op - ignore */ }
        }

        if (!g_visible) { Sleep(30); continue; }   // hidden pane: stay alive, burn nothing

        // embedded: keep filling the host pane as BRN resizes it
        if (embedded) {
            RECT rc; if (GetClientRect(g_parentWid, &rc) && rc.right > 0 && rc.bottom > 0 && (rc.right != g_W || rc.bottom != g_H)) MoveWindow(hwnd, 0, 0, rc.right, rc.bottom, TRUE);
        }
        // human keys -> same applyOp path. STANDALONE only: embedded, the app's page owns the
        // keyboard (it forwards semantic ops), so global-key sniffing can't eat BRN's typing.
        if (!embedded && GetForegroundWindow() == hwnd) {
            if (GetAsyncKeyState(VK_SPACE) & 1) { applyOp("play", curSec, json::object(), curSec, playing); }
            if (GetAsyncKeyState('S') & 1) { applyOp("split", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState(VK_DELETE) & 1) { applyOp("delete", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('O') & 1) { applyOp("trim_out", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('I') & 1) { applyOp("trim_in", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('G') & 1) { applyOp("group", curSec, json::object(), curSec, playing); }
        }
        // embedded: NO local clock - the app's playhead reports drive curSec (quiet seeks)
        if (playing && !embedded) { curSec += dt; if (curSec >= g_compDur) curSec = 0; }

        // STANDALONE preview: decode + composite the frame under the playhead
        if (!embedded && !g_track[0].empty() && curSec != lastComposed) { compose(curSec); uploadFrame(); lastComposed = curSec; }
        if (g_resize) { resizeD3D(); g_resize = false; }

        ImGui_ImplDX11_NewFrame(); ImGui_ImplWin32_NewFrame(); ImGui::NewFrame();
        ImGui::SetNextWindowPos({ 0, 0 }); ImGui::SetNextWindowSize({ (float)g_W, (float)g_H });
        ImGui::Begin("becky", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus | ImGuiWindowFlags_NoScrollbar | ImGuiWindowFlags_NoScrollWithMouse);

        if (!embedded) {
            ImGui::Text("becky-timeline   %s   %.1fs / %.0fs   compose %.2f ms (%.0f fps)",
                playing ? "PLAYING" : "paused", curSec, g_compDur, g_composeMs, g_composeMs > 0 ? 1000.0 / g_composeMs : 0);
            ImGui::SameLine(); ImGui::TextColored(g_group ? ImVec4(1, 0.82f, 0, 1) : ImVec4(0.5f, 0.5f, 0.5f, 1), "  [G]roup %s", g_group ? "ON" : "off");
            ImGui::SameLine(); ImGui::TextDisabled("  [S]plit  trim [I]/[O]  [Del]  |  drag=scrub  Ctrl+wheel=zoom  Space=play");
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

    ImGui_ImplDX11_Shutdown(); ImGui_ImplWin32_Shutdown(); ImGui::DestroyContext();
    if (g_frameSrv) g_frameSrv->Release(); if (g_frameTex) g_frameTex->Release();
    if (g_rtv) g_rtv->Release(); if (g_swap) g_swap->Release(); if (g_ctx) g_ctx->Release(); if (g_dev) g_dev->Release();
    for (auto& L : g_layer) { if (L.pipe) { gst_element_set_state(L.pipe, GST_STATE_NULL); gst_object_unref(L.bus); gst_object_unref(L.pipe); } }
    return 0;
}
