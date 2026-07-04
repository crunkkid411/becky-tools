// gst_scrubwin.c - the VISIBLE, interactive proof: drag anywhere in the window and the live
// 2-layer composite tracks your mouse instantly. Two independent GStreamer d3d11 decoders (proven
// 2325 fps in gst_scrub_indep) are each seeked to the playhead, downloaded, composited on CPU
// (layer A full + layer B PiP, alpha 0.5) and blitted with GDI. The title bar shows the per-frame
// compose time so "instant" is measurable, not just felt. Proxy-res (all-intra) media = a seek is
// one light decode. This is the interaction-latency test every "good enough" NLE failed for Jordan.
//
//   usage: gst_scrubwin.exe <fileA> <fileB>
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <stdio.h>
#include <stdlib.h>

static GstElement *g_pipe[2];
static GstBus     *g_bus[2];
static guint8     *g_buf = NULL;   // composited BGRA (GDI-native), top row first
static int         g_w = 0, g_h = 0;
static gint64      g_dur = 0;      // ns
static double      g_playhead = 0; // 0..1
static double      g_ms = 0;       // last compose time
static HWND        g_hwnd;

static void fwdslash(char *s) { for (; *s; ++s) if (*s == '\\') *s = '/'; }

static int seek_pull(int k, GstClockTime pos, GstVideoFrame *frame, GstSample **out) {
    gst_element_seek_simple(g_pipe[k], GST_FORMAT_TIME,
        (GstSeekFlags)(GST_SEEK_FLAG_FLUSH | GST_SEEK_FLAG_ACCURATE), (gint64)pos);
    GstMessage *m = gst_bus_timed_pop_filtered(g_bus[k], 5 * GST_SECOND,
        (GstMessageType)(GST_MESSAGE_ASYNC_DONE | GST_MESSAGE_ERROR));
    if (!m) return 0;
    int err = (GST_MESSAGE_TYPE(m) == GST_MESSAGE_ERROR); gst_message_unref(m);
    if (err) return 0;
    GstElement *sink = gst_bin_get_by_name(GST_BIN(g_pipe[k]), "s");
    GstSample *s = gst_app_sink_pull_preroll(GST_APP_SINK(sink));
    gst_object_unref(sink);
    if (!s) return 0;
    GstVideoInfo info;
    if (!gst_video_info_from_caps(&info, gst_sample_get_caps(s))) { gst_sample_unref(s); return 0; }
    if (!gst_video_frame_map(frame, &info, gst_sample_get_buffer(s), GST_MAP_READ)) { gst_sample_unref(s); return 0; }
    *out = s;
    return 1;
}

// Recompose the 2-layer frame at the current playhead into g_buf (BGRA). Times itself.
static void recompose(void) {
    GstClockTime pos = (GstClockTime)(g_playhead * (double)g_dur);
    LARGE_INTEGER f, a, b; QueryPerformanceFrequency(&f); QueryPerformanceCounter(&a);
    GstVideoFrame fa, fb; GstSample *sa = NULL, *sb = NULL;
    if (!seek_pull(0, pos, &fa, &sa)) return;
    if (!seek_pull(1, pos, &fb, &sb)) { gst_video_frame_unmap(&fa); gst_sample_unref(sa); return; }
    int wa = GST_VIDEO_FRAME_WIDTH(&fa), ha = GST_VIDEO_FRAME_HEIGHT(&fa);
    int wb = GST_VIDEO_FRAME_WIDTH(&fb), hb = GST_VIDEO_FRAME_HEIGHT(&fb);
    guint8 *da = GST_VIDEO_FRAME_PLANE_DATA(&fa, 0); int sta = GST_VIDEO_FRAME_PLANE_STRIDE(&fa, 0);
    guint8 *db = GST_VIDEO_FRAME_PLANE_DATA(&fb, 0); int stb = GST_VIDEO_FRAME_PLANE_STRIDE(&fb, 0);
    if (!g_buf) { g_w = wa; g_h = ha; g_buf = malloc((size_t)wa * ha * 4); }
    for (int y = 0; y < ha; y++) memcpy(g_buf + (size_t)y * wa * 4, da + (size_t)y * sta, (size_t)wa * 4);
    int pw = wb / 2, ph = hb / 2, ox = wa - pw - wa / 20, oy = ha / 20;   // PiP inset, top-right
    for (int y = 0; y < ph; y++) {
        int dy = oy + y; if (dy < 0 || dy >= ha) continue;
        for (int x = 0; x < pw; x++) {
            int dx = ox + x; if (dx < 0 || dx >= wa) continue;
            guint8 *src = db + (size_t)(y * 2) * stb + (size_t)(x * 2) * 4;
            guint8 *dst = g_buf + ((size_t)dy * wa + dx) * 4;
            for (int c = 0; c < 3; c++) dst[c] = (guint8)((dst[c] + src[c]) / 2);
        }
    }
    gst_video_frame_unmap(&fa); gst_video_frame_unmap(&fb); gst_sample_unref(sa); gst_sample_unref(sb);
    QueryPerformanceCounter(&b);
    g_ms = 1000.0 * (b.QuadPart - a.QuadPart) / f.QuadPart;
    char t[160];
    snprintf(t, sizeof t, "becky scrub proof  |  2 layers @ %.1fs  |  compose %.2f ms (%.0f fps)  |  DRAG to scrub",
             g_playhead * g_dur / GST_SECOND, g_ms, g_ms > 0 ? 1000.0 / g_ms : 0);
    SetWindowTextA(g_hwnd, t);
}

static LRESULT CALLBACK WndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    switch (m) {
    case WM_LBUTTONDOWN: case WM_MOUSEMOVE:
        if (m == WM_LBUTTONDOWN || (w & MK_LBUTTON)) {
            RECT rc; GetClientRect(h, &rc);
            int x = (short)LOWORD(l);
            g_playhead = rc.right > 0 ? (double)x / rc.right : 0;
            if (g_playhead < 0) g_playhead = 0; if (g_playhead > 1) g_playhead = 1;
            recompose(); InvalidateRect(h, NULL, FALSE);
        }
        return 0;
    case WM_PAINT: {
        PAINTSTRUCT ps; HDC hdc = BeginPaint(h, &ps);
        RECT rc; GetClientRect(h, &rc);
        int cw = rc.right, ch = rc.bottom, sb = 44, vh = ch - sb;
        if (g_buf) {
            BITMAPINFO bmi; ZeroMemory(&bmi, sizeof bmi);
            bmi.bmiHeader.biSize = sizeof(BITMAPINFOHEADER);
            bmi.bmiHeader.biWidth = g_w; bmi.bmiHeader.biHeight = -g_h;  // top-down
            bmi.bmiHeader.biPlanes = 1; bmi.bmiHeader.biBitCount = 32; bmi.bmiHeader.biCompression = BI_RGB;
            int dw = vh * g_w / g_h; if (dw > cw) dw = cw; int dx = (cw - dw) / 2;   // fit height, center
            SetStretchBltMode(hdc, HALFTONE);
            StretchDIBits(hdc, dx, 0, dw, vh, 0, 0, g_w, g_h, g_buf, &bmi, DIB_RGB_COLORS, SRCCOPY);
        }
        HBRUSH bar = CreateSolidBrush(RGB(24, 24, 28)); RECT sr = { 0, vh, cw, ch }; FillRect(hdc, &sr, bar); DeleteObject(bar);
        int px = (int)(g_playhead * cw);
        HPEN pen = CreatePen(PS_SOLID, 3, RGB(255, 210, 0)); HGDIOBJ op = SelectObject(hdc, pen);
        MoveToEx(hdc, px, vh, NULL); LineTo(hdc, px, ch); SelectObject(hdc, op); DeleteObject(pen);
        EndPaint(h, &ps);
        return 0;
    }
    case WM_DESTROY: PostQuitMessage(0); return 0;
    }
    return DefWindowProc(h, m, w, l);
}

int main(int argc, char **argv) {
    if (argc < 3) { fprintf(stderr, "usage: gst_scrubwin <fileA> <fileB>\n"); return 2; }
    fwdslash(argv[1]); fwdslash(argv[2]);
    gst_init(&argc, &argv);
    const char *tmpl =
        "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! d3d11convert ! "
        "video/x-raw(memory:D3D11Memory),format=BGRA ! d3d11download ! appsink name=s sync=false max-buffers=2";
    for (int k = 0; k < 2; k++) {
        char s[2048]; snprintf(s, sizeof s, tmpl, argv[1 + k]);
        GError *e = NULL; g_pipe[k] = gst_parse_launch(s, &e);
        if (!g_pipe[k] || e) { fprintf(stderr, "parse %d: %s\n", k, e ? e->message : "?"); return 3; }
        g_bus[k] = gst_element_get_bus(g_pipe[k]);
        gst_element_set_state(g_pipe[k], GST_STATE_PAUSED);
    }
    for (int k = 0; k < 2; k++)
        if (gst_element_get_state(g_pipe[k], NULL, NULL, 15 * GST_SECOND) != GST_STATE_CHANGE_SUCCESS) { fprintf(stderr, "preroll %d failed\n", k); return 4; }
    if (!gst_element_query_duration(g_pipe[0], GST_FORMAT_TIME, &g_dur) || g_dur <= 0) g_dur = 115 * GST_SECOND;

    WNDCLASSA wc; ZeroMemory(&wc, sizeof wc);
    wc.lpfnWndProc = WndProc; wc.hInstance = GetModuleHandle(NULL); wc.lpszClassName = "beckyscrub";
    wc.hCursor = LoadCursor(NULL, IDC_ARROW); wc.hbrBackground = (HBRUSH)(COLOR_WINDOW);
    RegisterClassA(&wc);
    g_hwnd = CreateWindowA("beckyscrub", "becky scrub proof", WS_OVERLAPPEDWINDOW,
        120, 60, 420, 800, NULL, NULL, wc.hInstance, NULL);
    g_playhead = 0.5; recompose();
    ShowWindow(g_hwnd, SW_SHOW); UpdateWindow(g_hwnd);

    MSG msg;
    while (GetMessage(&msg, NULL, 0, 0)) { TranslateMessage(&msg); DispatchMessage(&msg); }
    for (int k = 0; k < 2; k++) { gst_element_set_state(g_pipe[k], GST_STATE_NULL); gst_object_unref(g_bus[k]); gst_object_unref(g_pipe[k]); }
    return 0;
}
