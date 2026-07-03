// gst_scrub.c - raw d3d11 (NO GES) 2-layer decode+composite SCRUB benchmark.
//
// Isolates the GPU capability -- can NVDEC decode N streams + d3d11-composite + seek fast? -- from
// GES's per-seek engine overhead (which pinned GES at ~1 fps). This is THE core risk of the
// own-compositor architecture (lean decoders -> our own composite, no engine). Feed it ALL-INTRA
// proxies (every frame a keyframe) so a seek is one light decode: the real scrub shape. The d3d11
// GPU decoders are forced via GST_PLUGIN_FEATURE_RANK by the launcher.
//
//   usage: gst_scrub.exe <proxyA> <proxyB> [frames] [keyunit]
#include <gst/gst.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int cmp_double(const void *a, const void *b) {
    double d = *(const double *)a - *(const double *)b;
    return (d > 0) - (d < 0);
}

// gst_parse_launch treats '\' as an escape char, so a Windows path (X:\a\b) gets mangled.
// filesrc accepts forward slashes on Windows -> normalize before building the pipeline string.
static void fwdslash(char *s) { for (; *s; ++s) if (*s == '\\') *s = '/'; }

int main(int argc, char **argv) {
    if (argc < 3) { fprintf(stderr, "usage: gst_scrub <proxyA> <proxyB> [frames] [keyunit]\n"); return 2; }
    const int frames = (argc > 3) ? atoi(argv[3]) : 200;
    const int warm = 20;
    const int keyunit = (argc > 4 && strcmp(argv[4], "keyunit") == 0);
    const GstSeekFlags sflags = (GstSeekFlags)(GST_SEEK_FLAG_FLUSH |
        (keyunit ? GST_SEEK_FLAG_KEY_UNIT : GST_SEEK_FLAG_ACCURATE));

    gst_init(&argc, &argv);
    fwdslash(argv[1]);
    fwdslash(argv[2]);

    // 2 layers: each file -> parsebin -> d3d11 GPU decode -> d3d11compositor -> fakesink. A
    // compositor pulls (decodes) EVERY sink pad each frame regardless of z-order, so both streams
    // genuinely decode + composite. No GES, no graph reconfiguration on seek -- just a flush seek.
    char pipe[4096];
    snprintf(pipe, sizeof pipe,
        "d3d11compositor name=mix ! fakesink name=s sync=false async=false "
        "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! queue ! mix. "
        "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! queue ! mix.",
        argv[1], argv[2]);

    GError *err = NULL;
    GstElement *pel = gst_parse_launch(pipe, &err);
    if (!pel || err) { fprintf(stderr, "parse_launch failed: %s\n", err ? err->message : "?"); return 3; }

    GstBus *bus = gst_element_get_bus(pel);
    if (gst_element_set_state(pel, GST_STATE_PAUSED) == GST_STATE_CHANGE_FAILURE) {
        fprintf(stderr, "set PAUSED failed\n"); return 4;
    }
    if (gst_element_get_state(pel, NULL, NULL, 15 * GST_SECOND) != GST_STATE_CHANGE_SUCCESS) {
        fprintf(stderr, "preroll to PAUSED failed/timeout\n"); return 4;
    }

    const GstClockTime dur = 115 * GST_SECOND;  // inside the 120 s proxies
    double *ms = (double *)malloc(sizeof(double) * frames);
    int got = 0;
    for (int i = -warm; i < frames; i++) {
        int idx = i < 0 ? 0 : i;
        GstClockTime pos = (GstClockTime)((gdouble)dur * idx / frames);
        gint64 t0 = g_get_monotonic_time();
        gst_element_seek_simple(pel, GST_FORMAT_TIME, sflags, (gint64)pos);
        GstMessage *m = gst_bus_timed_pop_filtered(bus, 5 * GST_SECOND,
            (GstMessageType)(GST_MESSAGE_ASYNC_DONE | GST_MESSAGE_ERROR));
        gint64 t1 = g_get_monotonic_time();
        if (!m) { fprintf(stderr, "seek timeout at i=%d\n", i); break; }
        if (GST_MESSAGE_TYPE(m) == GST_MESSAGE_ERROR) {
            GError *e = NULL; gst_message_parse_error(m, &e, NULL);
            fprintf(stderr, "pipeline ERROR: %s\n", e ? e->message : "?");
            if (e) g_error_free(e);
            gst_message_unref(m); break;
        }
        gst_message_unref(m);
        if (i >= 0) ms[got++] = (double)(t1 - t0) / 1000.0;
    }
    gst_element_set_state(pel, GST_STATE_NULL);

    if (got > 0) {
        qsort(ms, got, sizeof(double), cmp_double);
        double sum = 0; for (int i = 0; i < got; i++) sum += ms[i];
        double avg = sum / got, p99 = ms[(int)(0.99 * (got - 1))], mx = ms[got - 1];
        printf("{\"engine\":\"raw-d3d11\",\"mode\":\"%s\",\"layers\":2,\"seeks\":%d,\"avg_ms\":%.3f,\"p99_ms\":%.3f,\"max_ms\":%.3f,\"fps\":%.1f}\n",
               keyunit ? "keyunit" : "accurate", got, avg, p99, mx, 1000.0 / avg);
        fprintf(stderr, "[raw-d3d11] 2-layer %s scrub: %.1f fps (%.2f ms avg, %.2f p99, %.2f max) over %d seeks\n",
                keyunit ? "keyunit" : "accurate", 1000.0 / avg, avg, p99, mx, got);
    } else {
        printf("{\"engine\":\"raw-d3d11\",\"error\":\"no timed seeks\"}\n");
    }
    free(ms);
    gst_object_unref(bus);
    gst_object_unref(pel);
    return 0;
}
