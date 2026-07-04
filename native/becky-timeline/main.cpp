// becky-timeline - the real thing: a native timeline window with a live 2-layer video composite.
//
// ImGui + ImSequencer timeline (proven 4000 fps in ../timeline-bench) over the proven video engine
// (../ges-bench: two INDEPENDENT GStreamer d3d11 decoders, each seeked to the playhead, composited).
// Scrub the timeline cursor OR press Space to play -> the video pane shows layer A (main) + layer B
// (PiP) composited at the playhead, live. All-intra proxies -> every seek is one light decode.
//
// This is v1 of the editor: 2 tracks, 2 clips, scrub + play. Multi-clip-per-track source switching,
// cut/split, and the becky editmodel/NDJSON bridge are the next increments.
//
//   usage: becky-timeline.exe <proxyA.mp4> <proxyB.mp4>
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <GL/gl.h>
#include "imgui.h"
#include "imgui_impl_win32.h"
#include "imgui_impl_opengl3.h"
#include "ImSequencer.h"

#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <cstdio>
#include <cstdint>
#include <vector>
#include <string>

// ---------------- GStreamer 2-layer composite -> RGBA ----------------
struct Layer { GstElement* pipe = nullptr; GstBus* bus = nullptr; double inpoint = 0; };

static Layer g_layer[2];
static std::vector<uint8_t> g_rgba;   // composited RGBA
static int g_vw = 0, g_vh = 0;
static double g_composeMs = 0;

static void fwdslash(std::string& s){ for (auto& c : s) if (c == '\\') c = '/'; }

static bool layerInit(Layer& L, std::string file, double inpoint) {
    fwdslash(file);
    char s[2048];
    snprintf(s, sizeof s,
        "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! d3d11convert ! "
        "video/x-raw(memory:D3D11Memory),format=RGBA ! d3d11download ! appsink name=s sync=false max-buffers=2",
        file.c_str());
    GError* e = nullptr; L.pipe = gst_parse_launch(s, &e);
    if (!L.pipe || e) { fprintf(stderr, "parse: %s\n", e ? e->message : "?"); return false; }
    L.bus = gst_element_get_bus(L.pipe); L.inpoint = inpoint;
    gst_element_set_state(L.pipe, GST_STATE_PAUSED);
    return gst_element_get_state(L.pipe, nullptr, nullptr, 15 * GST_SECOND) == GST_STATE_CHANGE_SUCCESS;
}

// seek one layer to (inpoint + t) and map its frame; caller unmaps+unrefs.
static bool layerFrame(Layer& L, double t, GstVideoFrame* f, GstSample** out) {
    GstClockTime pos = (GstClockTime)((L.inpoint + t) * GST_SECOND);
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

// Composite layer A (full) + layer B (PiP, top-right, alpha 0.5) into g_rgba at comp time t.
static void compose(double t) {
    LARGE_INTEGER fq, a, b; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&a);
    GstVideoFrame fa, fb; GstSample* sa = nullptr; GstSample* sb = nullptr;
    if (!layerFrame(g_layer[0], t, &fa, &sa)) return;
    if (!layerFrame(g_layer[1], t, &fb, &sb)) { gst_video_frame_unmap(&fa); gst_sample_unref(sa); return; }
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

// ---------------- ImSequencer model: 2 clips (track A main, track B PiP), both span the comp timeline ----------------
struct Seq : public ImSequencer::SequenceInterface {
    int fps = 30, dur = 115;   // comp seconds
    int GetFrameMin() const override { return 0; }
    int GetFrameMax() const override { return dur * fps; }
    int GetItemCount() const override { return 2; }
    int GetItemTypeCount() const override { return 1; }
    const char* GetItemTypeName(int) const override { return "clip"; }
    const char* GetItemLabel(int i) const override { return i == 0 ? "A - main" : "B - PiP"; }
    void Get(int i, int** s, int** e, int* type, unsigned int* color) override {
        static int s0 = 0, e0 = 0; s0 = 0; e0 = GetFrameMax();
        if (s) *s = &s0; if (e) *e = &e0; if (type) *type = 0;
        if (color) *color = i == 0 ? 0xFF3A7BFF : 0xFF00C878;
    }
};

// ---------------- WGL boilerplate (from ../timeline-bench) ----------------
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

int main(int argc, char** argv) {
    if (argc < 3) { fprintf(stderr, "usage: becky-timeline <proxyA> <proxyB>\n"); return 2; }
    gst_init(&argc, &argv);
    if (!layerInit(g_layer[0], argv[1], 0.0) || !layerInit(g_layer[1], argv[2], 0.0)) { fprintf(stderr, "layer init failed\n"); return 3; }

    WNDCLASSEXW wc = { sizeof wc, CS_OWNDC, WndProc, 0, 0, GetModuleHandle(nullptr), nullptr, LoadCursor(nullptr, IDC_ARROW), nullptr, nullptr, L"beckytl", nullptr };
    RegisterClassExW(&wc);
    HWND hwnd = CreateWindowW(wc.lpszClassName, L"becky-timeline", WS_OVERLAPPEDWINDOW, 80, 40, g_W, g_H, nullptr, nullptr, wc.hInstance, nullptr);
    CreateGL(hwnd);
    typedef BOOL(WINAPI* SI)(int); if (auto si = (SI)wglGetProcAddress("wglSwapIntervalEXT")) si(1);  // vsync on: smooth playback
    ShowWindow(hwnd, SW_SHOW); UpdateWindow(hwnd);

    IMGUI_CHECKVERSION(); ImGui::CreateContext(); ImGui::GetIO().IniFilename = nullptr; ImGui::StyleColorsDark();
    ImGui_ImplWin32_InitForOpenGL(hwnd); ImGui_ImplOpenGL3_Init(nullptr);
    glGenTextures(1, &g_tex);

    Seq seq; int curFrame = 0, firstFrame = 0, selected = -1; bool expanded = true, playing = false, dirty = true;
    int lastComposed = -1; double playSec = 0;
    LARGE_INTEGER fq, prev; QueryPerformanceFrequency(&fq); QueryPerformanceCounter(&prev);

    bool run = true;
    while (run) {
        MSG msg; while (PeekMessage(&msg, nullptr, 0, 0, PM_REMOVE)) { TranslateMessage(&msg); DispatchMessage(&msg); if (msg.message == WM_QUIT) run = false; }
        if (!run) break;
        LARGE_INTEGER now; QueryPerformanceCounter(&now);
        double dt = (double)(now.QuadPart - prev.QuadPart) / fq.QuadPart; prev = now;

        if (GetForegroundWindow() == hwnd && (GetAsyncKeyState(VK_SPACE) & 1)) playing = !playing;
        if (playing) { playSec += dt; if (playSec * seq.fps >= seq.GetFrameMax()) playSec = 0; curFrame = (int)(playSec * seq.fps); dirty = true; }

        if (dirty || curFrame != lastComposed) {
            compose((double)curFrame / seq.fps);
            if (!g_rgba.empty()) {
                glBindTexture(GL_TEXTURE_2D, g_tex);
                glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR);
                glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
                glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA, g_vw, g_vh, 0, GL_RGBA, GL_UNSIGNED_BYTE, g_rgba.data());
            }
            lastComposed = curFrame; dirty = false;
        }

        ImGui_ImplOpenGL3_NewFrame(); ImGui_ImplWin32_NewFrame(); ImGui::NewFrame();
        ImGui::SetNextWindowPos({ 0, 0 }); ImGui::SetNextWindowSize({ (float)g_W, (float)g_H });
        ImGui::Begin("becky", nullptr, ImGuiWindowFlags_NoTitleBar | ImGuiWindowFlags_NoResize | ImGuiWindowFlags_NoMove | ImGuiWindowFlags_NoBringToFrontOnFocus);
        ImGui::Text("becky-timeline   %s   %.1fs / %ds   compose %.2f ms (%.0f fps)   [Space = play]   drag the timeline cursor to scrub",
            playing ? "PLAYING" : "paused", (double)curFrame / seq.fps, seq.dur, g_composeMs, g_composeMs > 0 ? 1000.0 / g_composeMs : 0);

        // video pane: fit the composite into the upper area
        if (g_vw > 0) {
            float availW = ImGui::GetContentRegionAvail().x, availH = g_H * 0.62f;
            float sc = availH / g_vh; if (g_vw * sc > availW) sc = availW / g_vw;
            float iw = g_vw * sc, ih = g_vh * sc;
            ImGui::SetCursorPosX((availW - iw) * 0.5f + ImGui::GetCursorPosX());
            ImGui::Image((ImTextureID)(intptr_t)g_tex, { iw, ih }, { 0, 0 }, { 1, 1 });
        }
        int before = curFrame;
        ImSequencer::Sequencer(&seq, &curFrame, &expanded, &selected, &firstFrame,
            ImSequencer::SEQUENCER_CHANGE_FRAME);
        if (curFrame != before) { playing = false; playSec = (double)curFrame / seq.fps; dirty = true; }   // user scrubbed
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
