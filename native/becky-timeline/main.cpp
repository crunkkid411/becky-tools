// becky-timeline - native NLE editor over the proven video engine (../ges-bench).
//
// Two clip TRACKS (A main, B PiP), each a list of clips. Two independent GStreamer d3d11 decoders,
// each seeked to the source-time the playhead maps to, composited (A full + B PiP). Custom ImGui
// track timeline. HUMAN and AI drive the SAME edit path (applyOp): human via keys/mouse, AI via
// NDJSON on stdin ({"op":"split","t":30}); every edit emits the timeline state as NDJSON on stdout.
//
// controls: drag = scrub | Space = play | S = split | Del = delete | I/O = trim in/out | G = group
// group ON -> split/delete/trim apply to BOTH tracks at the same comp time, so the layers stay synced.
//
// v5: two-track clip model + group toggle + AI-in-the-loop NDJSON bridge.
// Next: multi-SOURCE track, full-res GPU composite (drop CPU readback), Becky Review Native embed.
//
//   usage: becky-timeline.exe <proxyA.mp4> <proxyB.mp4>
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <GL/gl.h>
#include "imgui.h"
#include "imgui_impl_win32.h"
#include "imgui_impl_opengl3.h"
#include "json.hpp"

#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <cstdio>
#include <cstdint>
#include <vector>
#include <string>
#include <thread>
#include <mutex>
#include <iostream>
using json = nlohmann::json;

// ---------------- GStreamer decoder (one per layer) ----------------
struct Layer { GstElement* pipe = nullptr; GstBus* bus = nullptr; };
static Layer g_layer[2];
static std::vector<uint8_t> g_rgba;
static int g_vw = 0, g_vh = 0;
static double g_composeMs = 0;

static void fwdslash(std::string& s){ for (auto& c : s) if (c == '\\') c = '/'; }

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

static bool layerFrame(Layer& L, double srcSec, GstVideoFrame* f, GstSample** out) {
    GstClockTime pos = (GstClockTime)(srcSec * GST_SECOND);
    gst_element_seek_simple(L.pipe, GST_FORMAT_TIME,
        (GstSeekFlags)(GST_SEEK_FLAG_FLUSH | GST_SEEK_FLAG_ACCURATE), (gint64)pos);
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

// ---------------- two clip tracks ----------------
struct Clip { double in, out, compStart; std::string label; };
static std::vector<Clip> g_track[2];   // 0 = A (main), 1 = B (PiP)
static double g_compDur = 0;
static bool g_group = true;            // grouped by default -> layers stay in sync

static double mapTrack(int tr, double t) {
    for (auto& c : g_track[tr]) { double d = c.out - c.in; if (t >= c.compStart && t < c.compStart + d) return c.in + (t - c.compStart); }
    if (!g_track[tr].empty()) { auto& c = g_track[tr].back(); return (t < 0) ? c.in : c.out; }
    return 0;
}
static void relabel(int tr) {
    const char* p = tr == 0 ? "clip " : "pip ";
    for (size_t i = 0; i < g_track[tr].size(); i++) g_track[tr][i].label = p + std::to_string(i + 1);
}
static void splitTrack(int tr, double t) {
    for (size_t i = 0; i < g_track[tr].size(); i++) { Clip& c = g_track[tr][i]; double d = c.out - c.in;
        if (t > c.compStart + 0.05 && t < c.compStart + d - 0.05) {
            double srcT = c.in + (t - c.compStart);
            Clip right{ srcT, c.out, t, "" }; c.out = srcT;
            g_track[tr].insert(g_track[tr].begin() + i + 1, right); relabel(tr); return; } }
}
static void deleteTrack(int tr, double t) {
    for (size_t i = 0; i < g_track[tr].size(); i++) { Clip& c = g_track[tr][i]; double d = c.out - c.in;
        if (t >= c.compStart && t < c.compStart + d) {
            g_track[tr].erase(g_track[tr].begin() + i);
            for (size_t j = i; j < g_track[tr].size(); j++) g_track[tr][j].compStart -= d;
            relabel(tr); return; } }
}
static void recomputeDur() {   // comp duration = longest track
    g_compDur = 0;
    for (int tr = 0; tr < 2; tr++) if (!g_track[tr].empty()) { auto& c = g_track[tr].back(); g_compDur = (c.compStart + (c.out - c.in) > g_compDur) ? c.compStart + (c.out - c.in) : g_compDur; }
}

static void compose(double t) {
    LARGE_INTEGER fq, a, b; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&a);
    GstVideoFrame fa, fb; GstSample* sa = nullptr; GstSample* sb = nullptr;
    if (!layerFrame(g_layer[0], mapTrack(0, t), &fa, &sa)) return;
    if (!layerFrame(g_layer[1], mapTrack(1, t), &fb, &sb)) { gst_video_frame_unmap(&fa); gst_sample_unref(sa); return; }
    int wa = GST_VIDEO_FRAME_WIDTH(&fa), ha = GST_VIDEO_FRAME_HEIGHT(&fa);
    int wb = GST_VIDEO_FRAME_WIDTH(&fb), hb = GST_VIDEO_FRAME_HEIGHT(&fb);
    uint8_t* da = (uint8_t*)GST_VIDEO_FRAME_PLANE_DATA(&fa, 0); int sta = GST_VIDEO_FRAME_PLANE_STRIDE(&fa, 0);
    uint8_t* db = (uint8_t*)GST_VIDEO_FRAME_PLANE_DATA(&fb, 0); int stb = GST_VIDEO_FRAME_PLANE_STRIDE(&fb, 0);
    g_vw = wa; g_vh = ha; g_rgba.resize((size_t)wa * ha * 4);
    for (int y = 0; y < ha; y++) memcpy(&g_rgba[(size_t)y * wa * 4], da + (size_t)y * sta, (size_t)wa * 4);
    int pw = wb / 2, ph = hb / 2, ox = wa - pw - wa / 20, oy = ha / 20;
    for (int y = 0; y < ph; y++) { int dy = oy + y; if (dy < 0 || dy >= ha) continue;
        for (int x = 0; x < pw; x++) { int dx = ox + x; if (dx < 0 || dx >= wa) continue;
            uint8_t* src = db + (size_t)(y * 2) * stb + (size_t)(x * 2) * 4;
            uint8_t* dst = &g_rgba[((size_t)dy * wa + dx) * 4];
            for (int c = 0; c < 3; c++) dst[c] = (uint8_t)((dst[c] + src[c]) / 2); } }
    gst_video_frame_unmap(&fa); gst_video_frame_unmap(&fb); gst_sample_unref(sa); gst_sample_unref(sb);
    QueryPerformanceCounter(&b); g_composeMs = 1000.0 * (b.QuadPart - a.QuadPart) / fq.QuadPart;
}

// ---------------- AI-in-the-loop: NDJSON edit ops on stdin ----------------
static std::mutex g_mx; static std::vector<json> g_pending;
static void stdinReader() {
    std::string line;
    while (std::getline(std::cin, line)) {
        if (line.empty()) continue;
        try { json j = json::parse(line); std::lock_guard<std::mutex> lk(g_mx); g_pending.push_back(j); } catch (...) {}
    }
}
static void emitState(double curSec, bool playing) {   // becky's AI reads this to see the result
    json s; s["t"] = curSec; s["dur"] = g_compDur; s["playing"] = playing; s["group"] = g_group;
    for (int tr = 0; tr < 2; tr++) { json arr = json::array();
        for (auto& c : g_track[tr]) arr.push_back({ {"in", c.in}, {"out", c.out}, {"start", c.compStart} });
        s[tr == 0 ? "trackA" : "trackB"] = arr; }
    std::cout << s.dump() << std::endl; std::cout.flush();
}

// one edit path for BOTH human and AI. returns true if the model changed.
static bool applyOp(const std::string& op, double t, const json& j, double& curSec, bool& playing) {
    auto clampT = [&](double x){ return x < 0 ? 0 : (x > g_compDur ? g_compDur : x); };
    if (op == "seek") { curSec = clampT(t); return true; }
    if (op == "play") { playing = j.value("on", !playing); return true; }
    if (op == "group") { g_group = j.value("on", !g_group); return true; }
    if (op == "split") { splitTrack(0, t); if (g_group) splitTrack(1, t); return true; }
    if (op == "delete") { deleteTrack(0, t); if (g_group) deleteTrack(1, t); recomputeDur(); curSec = clampT(curSec); return true; }
    if (op == "trim_out") { splitTrack(0, t); deleteTrack(0, t); if (g_group) { splitTrack(1, t); deleteTrack(1, t); } recomputeDur(); curSec = clampT(curSec); return true; }
    if (op == "trim_in") { splitTrack(0, t); deleteTrack(0, t - 0.02); if (g_group) { splitTrack(1, t); deleteTrack(1, t - 0.02); } recomputeDur(); curSec = clampT(curSec); return true; }
    return false;
}

// ---------------- WGL ----------------
static HGLRC g_rc; static HDC g_dc; static int g_W = 1100, g_H = 900; static GLuint g_tex = 0;
static bool CreateGL(HWND h) {
    g_dc = GetDC(h);
    PIXELFORMATDESCRIPTOR pfd = {}; pfd.nSize = sizeof pfd; pfd.nVersion = 1;
    pfd.dwFlags = PFD_DRAW_TO_WINDOW | PFD_SUPPORT_OPENGL | PFD_DOUBLEBUFFER;
    pfd.iPixelType = PFD_TYPE_RGBA; pfd.cColorBits = 32;
    int pf = ChoosePixelFormat(g_dc, &pfd); SetPixelFormat(g_dc, pf, &pfd);
    g_rc = wglCreateContext(g_dc); wglMakeCurrent(g_dc, g_rc);
    return g_rc != nullptr;
}
extern IMGUI_IMPL_API LRESULT ImGui_ImplWin32_WndProcHandler(HWND, UINT, WPARAM, LPARAM);
static LRESULT WINAPI WndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    if (ImGui_ImplWin32_WndProcHandler(h, m, w, l)) return true;
    if (m == WM_SIZE && w != SIZE_MINIMIZED) { g_W = LOWORD(l); g_H = HIWORD(l); }
    if (m == WM_DESTROY) { PostQuitMessage(0); return 0; }
    return DefWindowProcW(h, m, w, l);
}

static void drawTrack(ImDrawList* dl, float tlX, float pps, float y, float trackH, int tr, ImU32 fill) {
    for (auto& c : g_track[tr]) {
        float x0 = tlX + (float)(c.compStart * pps), x1 = tlX + (float)((c.compStart + (c.out - c.in)) * pps);
        dl->AddRectFilled({ x0 + 1, y }, { x1 - 1, y + trackH }, fill, 3);
        dl->AddRect({ x0 + 1, y }, { x1 - 1, y + trackH }, IM_COL32(255, 255, 255, 90), 3);
        dl->AddText({ x0 + 5, y + 8 }, IM_COL32(255, 255, 255, 230), c.label.c_str());
    }
}
static double drawTimeline(double curSec, bool& scrubbed) {
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float tlX = p.x + 8, tlW = ImGui::GetContentRegionAvail().x - 16;
    float rulerH = 20, trackH = 34, gap = 6;
    float pps = (g_compDur > 0) ? tlW / (float)g_compDur : 1;
    float ay = p.y + rulerH + gap, by = ay + trackH + gap, bot = by + trackH;
    dl->AddRectFilled(p, { p.x + tlW + 16, bot + 4 }, IM_COL32(20, 22, 26, 255));
    for (int s = 0; s <= (int)g_compDur; s += 5) {
        float x = tlX + s * pps; dl->AddLine({ x, p.y }, { x, p.y + rulerH }, IM_COL32(90, 94, 104, 255));
        char b[8]; snprintf(b, 8, "%d", s); dl->AddText({ x + 2, p.y + 3 }, IM_COL32(150, 155, 165, 255), b);
    }
    drawTrack(dl, tlX, pps, ay, trackH, 0, IM_COL32(58, 123, 255, 255));   // A main
    drawTrack(dl, tlX, pps, by, trackH, 1, IM_COL32(0, 200, 120, 255));    // B PiP
    float px = tlX + (float)curSec * pps;
    dl->AddLine({ px, p.y }, { px, bot }, IM_COL32(255, 210, 0, 255), 2);

    ImGui::SetCursorScreenPos(p);
    ImGui::InvisibleButton("tl", { tlW + 16, bot - p.y + 4 });
    scrubbed = false;
    if (ImGui::IsItemActive()) {
        double s = (ImGui::GetIO().MousePos.x - tlX) / pps;
        if (s < 0) s = 0; if (s > g_compDur) s = g_compDur;
        scrubbed = true; return s;
    }
    return curSec;
}

int main(int argc, char** argv) {
    if (argc < 3) { fprintf(stderr, "usage: becky-timeline <proxyA> <proxyB>\n"); return 2; }
    gst_init(&argc, &argv);
    if (!layerInit(g_layer[0], argv[1]) || !layerInit(g_layer[1], argv[2])) { fprintf(stderr, "layer init failed\n"); return 3; }

    // Track A = 4 segments of source A; Track B = the PiP overlay (one clip spanning the reel).
    struct Seg { double in, out; }; Seg segs[] = { {5,25}, {40,60}, {75,95}, {100,115} };
    double cs = 0; for (auto& s : segs) { g_track[0].push_back({ s.in, s.out, cs, "" }); cs += s.out - s.in; }
    g_track[1].push_back({ 0, cs, 0, "" });
    recomputeDur(); relabel(0); relabel(1);

    std::thread(stdinReader).detach();   // AI-in-the-loop: NDJSON ops on stdin

    WNDCLASSEXW wc = { sizeof wc, CS_OWNDC, WndProc, 0, 0, GetModuleHandle(nullptr), nullptr, LoadCursor(nullptr, IDC_ARROW), nullptr, nullptr, L"beckytl", nullptr };
    RegisterClassExW(&wc);
    HWND hwnd = CreateWindowW(wc.lpszClassName, L"becky-timeline", WS_OVERLAPPEDWINDOW, 80, 40, g_W, g_H, nullptr, nullptr, wc.hInstance, nullptr);
    CreateGL(hwnd);
    typedef BOOL(WINAPI* SI)(int); if (auto si = (SI)wglGetProcAddress("wglSwapIntervalEXT")) si(1);
    ShowWindow(hwnd, SW_SHOW); UpdateWindow(hwnd);

    IMGUI_CHECKVERSION(); ImGui::CreateContext(); ImGui::GetIO().IniFilename = nullptr; ImGui::StyleColorsDark();
    ImGui_ImplWin32_InitForOpenGL(hwnd); ImGui_ImplOpenGL3_Init(nullptr);
    glGenTextures(1, &g_tex);

    double curSec = 0, lastComposed = -1; bool playing = false;
    LARGE_INTEGER fq, prev; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&prev);

    bool run = true;
    while (run) {
        MSG msg; while (PeekMessage(&msg, nullptr, 0, 0, PM_REMOVE)) { TranslateMessage(&msg); DispatchMessage(&msg); if (msg.message == WM_QUIT) run = false; }
        if (!run) break;
        LARGE_INTEGER now; QueryPerformanceCounter(&now);
        double dt = (double)(now.QuadPart - prev.QuadPart) / fq.QuadPart; prev = now;

        // AI ops from stdin
        std::vector<json> ops; { std::lock_guard<std::mutex> lk(g_mx); ops.swap(g_pending); }
        for (auto& j : ops) { std::string op = j.value("op", std::string()); double t = j.value("t", curSec);
            if (applyOp(op, t, j, curSec, playing)) { lastComposed = -1; emitState(curSec, playing); } }

        // human keys -> same applyOp path
        if (GetForegroundWindow() == hwnd) {
            if (GetAsyncKeyState(VK_SPACE) & 1) { applyOp("play", curSec, json::object(), curSec, playing); }
            if (GetAsyncKeyState('S') & 1) { applyOp("split", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState(VK_DELETE) & 1) { applyOp("delete", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('O') & 1) { applyOp("trim_out", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('I') & 1) { applyOp("trim_in", curSec, json(), curSec, playing); lastComposed = -1; emitState(curSec, playing); }
            if (GetAsyncKeyState('G') & 1) { applyOp("group", curSec, json::object(), curSec, playing); }
        }
        if (playing) { curSec += dt; if (curSec >= g_compDur) curSec = 0; }

        if (curSec != lastComposed) {
            compose(curSec);
            if (!g_rgba.empty()) {
                glBindTexture(GL_TEXTURE_2D, g_tex);
                glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
                glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
                glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, g_vw, g_vh, 0, GL_RGBA, GL_UNSIGNED_BYTE, g_rgba.data());
            }
            lastComposed = curSec;
        }

        ImGui_ImplOpenGL3_NewFrame(); ImGui_ImplWin32_NewFrame(); ImGui::NewFrame();
        ImGui::SetNextWindowPos({ 0, 0 }); ImGui::SetNextWindowSize({ (float)g_W, (float)g_H });
        ImGui::Begin("becky", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus);
        ImGui::Text("becky-timeline   %s   %.1fs / %.0fs   compose %.2f ms (%.0f fps)",
            playing ? "PLAYING" : "paused", curSec, g_compDur, g_composeMs, g_composeMs > 0 ? 1000.0 / g_composeMs : 0);
        ImGui::SameLine(); ImGui::TextColored(g_group ? ImVec4(1, 0.82f, 0, 1) : ImVec4(0.5f, 0.5f, 0.5f, 1), "  [G]roup %s", g_group ? "ON" : "off");
        ImGui::SameLine(); ImGui::TextDisabled("  [S]plit  trim [I]/[O]  [Del]  |  drag=scrub  Space=play");

        if (g_vw > 0) {
            float availW = ImGui::GetContentRegionAvail().x, availH = g_H * 0.58f;
            float sc = availH / g_vh; if (g_vw * sc > availW) sc = availW / g_vw;
            float iw = g_vw * sc, ih = g_vh * sc;
            ImGui::SetCursorPosX((availW - iw) * 0.5f + ImGui::GetCursorPosX());
            ImGui::Image((ImTextureID)(intptr_t)g_tex, { iw, ih });
            ImGui::Dummy({ 0, 6 });
        }
        bool scrubbed = false;
        double s = drawTimeline(curSec, scrubbed);
        if (scrubbed) { curSec = s; playing = false; }
        ImGui::End();

        ImGui::Render();
        glViewport(0, 0, g_W, g_H); glClearColor(0.06f, 0.07f, 0.09f, 1); glClear(GL_COLOR_BUFFER_BIT);
        ImGui_ImplOpenGL3_RenderDrawData(ImGui::GetDrawData());
        SwapBuffers(g_dc);
    }

    ImGui_ImplOpenGL3_Shutdown(); ImGui_ImplWin32_Shutdown(); ImGui::DestroyContext();
    for (auto& L : g_layer) { if (L.pipe) { gst_element_set_state(L.pipe, GST_STATE_NULL); gst_object_unref(L.bus); gst_object_unref(L.pipe); } }
    return 0;
}
