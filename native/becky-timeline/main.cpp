// becky-timeline - native NLE editor: a real multi-clip timeline over the proven video engine.
//
// Track A = a sequence of clips (segments of a source, laid end-to-end); Track B = a PiP overlay.
// Two independent GStreamer d3d11 decoders (../ges-bench, proven 2325 fps), each seeked to the
// source-time the playhead maps to, composited (A full + B PiP). Custom ImGui track timeline:
// drag = scrub, Space = play. All-intra proxies -> every seek is one light decode.
//
// v2: real multi-clip track + custom NLE timeline. Next: multi-SOURCE track (pre-warm on cut),
// cut/split/trim, GPU composite for full-res, editmodel/NDJSON bridge.
//
//   usage: becky-timeline.exe <proxyA.mp4> <proxyB.mp4>
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <GL/gl.h>
#include "imgui.h"
#include "imgui_impl_win32.h"
#include "imgui_impl_opengl3.h"

#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <cstdio>
#include <cstdint>
#include <vector>
#include <string>

// ---------------- GStreamer decoder (one per layer) ----------------
struct Layer { GstElement* pipe = nullptr; GstBus* bus = nullptr; };
static Layer g_layer[2];                 // 0 = track A source, 1 = track B (PiP) source
static std::vector<uint8_t> g_rgba;      // composited RGBA
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

// seek a layer to an ABSOLUTE source time and map its frame; caller unmaps+unrefs.
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

// ---------------- clip model ----------------
struct Clip { double in, out, compStart; const char* label; };  // in/out = source seconds
static std::vector<Clip> g_trackA;   // laid end-to-end on the comp timeline
static Clip g_trackB;                // one PiP clip spanning the comp timeline
static double g_compDur = 0;

// comp seconds -> source seconds on track A (holds the last clip past the end).
static double mapTrackA(double t) {
    for (auto& c : g_trackA) { double d = c.out - c.in; if (t >= c.compStart && t < c.compStart + d) return c.in + (t - c.compStart); }
    if (!g_trackA.empty()) { auto& c = g_trackA.back(); return (t < 0) ? c.in : c.out; }
    return 0;
}

// Composite track A (full) + track B (PiP top-right, alpha 0.5) at comp time t into g_rgba.
static void compose(double t) {
    LARGE_INTEGER fq, a, b; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&a);
    GstVideoFrame fa, fb; GstSample* sa = nullptr; GstSample* sb = nullptr;
    if (!layerFrame(g_layer[0], mapTrackA(t), &fa, &sa)) return;
    double bt = (t >= g_trackB.compStart) ? g_trackB.in + (t - g_trackB.compStart) : g_trackB.in;
    if (!layerFrame(g_layer[1], bt, &fb, &sb)) { gst_video_frame_unmap(&fa); gst_sample_unref(sa); return; }
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

// ---------------- WGL boilerplate ----------------
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

// draw the custom NLE timeline; sets scrubbed + returns the scrubbed comp-second while dragging.
static double drawTimeline(double curSec, bool& scrubbed) {
    ImDrawList* dl = ImGui::GetWindowDrawList();
    ImVec2 p = ImGui::GetCursorScreenPos();
    float tlX = p.x + 8, tlW = ImGui::GetContentRegionAvail().x - 16;
    float rulerH = 20, trackH = 34, gap = 6;
    float pps = (g_compDur > 0) ? tlW / (float)g_compDur : 1;
    float ay = p.y + rulerH + gap, by = ay + trackH + gap, bot = by + trackH;
    dl->AddRectFilled(p, { p.x + tlW + 16, bot + 4 }, IM_COL32(20, 22, 26, 255));
    for (int s = 0; s <= (int)g_compDur; s += 5) {   // ruler ticks every 5s
        float x = tlX + s * pps; dl->AddLine({ x, p.y }, { x, p.y + rulerH }, IM_COL32(90, 94, 104, 255));
        char b[8]; snprintf(b, 8, "%d", s); dl->AddText({ x + 2, p.y + 3 }, IM_COL32(150, 155, 165, 255), b);
    }
    for (auto& c : g_trackA) {   // track A clips
        float x0 = tlX + c.compStart * pps, x1 = tlX + (c.compStart + (c.out - c.in)) * pps;
        dl->AddRectFilled({ x0 + 1, ay }, { x1 - 1, ay + trackH }, IM_COL32(58, 123, 255, 255), 3);
        dl->AddRect({ x0 + 1, ay }, { x1 - 1, ay + trackH }, IM_COL32(180, 200, 255, 200), 3);
        dl->AddText({ x0 + 5, ay + 8 }, IM_COL32(255, 255, 255, 230), c.label);
    }
    float bx0 = tlX + (float)(g_trackB.compStart * pps);   // track B (PiP)
    float bx1 = tlX + (float)((g_trackB.compStart + (g_trackB.out - g_trackB.in)) * pps);
    dl->AddRectFilled({ bx0 + 1, by }, { bx1 - 1, by + trackH }, IM_COL32(0, 200, 120, 255), 3);
    dl->AddText({ bx0 + 5, by + 8 }, IM_COL32(255, 255, 255, 230), g_trackB.label);
    float px = tlX + (float)curSec * pps;   // playhead
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

    // Track A = a real reel: 4 segments of source A laid end-to-end. (Same-source clips scrub with
    // no reload; multi-SOURCE with a pre-warmed decoder-per-source is the next increment.)
    struct Seg { double in, out; const char* label; };
    Seg segs[] = { {5,25,"clip 1"}, {40,60,"clip 2"}, {75,95,"clip 3"}, {100,115,"clip 4"} };
    double cs = 0;
    for (auto& s : segs) { g_trackA.push_back({ s.in, s.out, cs, s.label }); cs += s.out - s.in; }
    g_compDur = cs;
    g_trackB = { 0, g_compDur, 0, "PiP (source B)" };   // PiP overlay spans the whole comp timeline

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

        if (GetForegroundWindow() == hwnd && (GetAsyncKeyState(VK_SPACE) & 1)) playing = !playing;
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
        ImGui::Text("becky-timeline   %s   %.1fs / %.0fs   compose %.2f ms (%.0f fps)   [Space = play]   drag the timeline to scrub",
            playing ? "PLAYING" : "paused", curSec, g_compDur, g_composeMs, g_composeMs > 0 ? 1000.0 / g_composeMs : 0);

        if (g_vw > 0) {
            float availW = ImGui::GetContentRegionAvail().x, availH = g_H * 0.60f;
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
