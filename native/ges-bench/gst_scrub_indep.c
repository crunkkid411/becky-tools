// gst_scrub_indep.c - N INDEPENDENT d3d11 decoders scrub benchmark (the own-compositor model).
//
// gst_scrub.c routed 2 layers through GStreamer's d3d11compositor and its aggregator refused to
// forward a flush-seek to both branches -- the same shared-coordination trap that pinned GES at
// ~1 fps. The own-compositor design does NOT share an aggregator: each layer is its own decoder,
// seeked directly. This measures exactly that: N single-branch (filesrc->parsebin->d3d11h264dec->
// fakesink) pipelines. Per scrub position we FIRE all N seeks (decoders run concurrently) then
// collect all N prerolls -> wall-clock = time until every layer's frame is ready = realistic
// N-layer scrub latency. The composite itself is a trivial shader (not measured here).
//
//   usage: gst_scrub_indep <frames> <accurate|keyunit> <file1> <file2> [file3...]
#include <gst/gst.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define MAXN 8

static int cmp_double(const void *a, const void *b) {
    double d = *(const double *)a - *(const double *)b;
    return (d > 0) - (d < 0);
}
// gst_parse_launch treats '\' as escape -> normalize Windows paths to forward slashes.
static void fwdslash(char *s) { for (; *s; ++s) if (*s == '\\') *s = '/'; }

int main(int argc, char **argv) {
    if (argc < 5) { fprintf(stderr, "usage: gst_scrub_indep <frames> <accurate|keyunit> <file1> <file2> [file3...]\n"); return 2; }
    const int frames = atoi(argv[1]);
    const int keyunit = (strcmp(argv[2], "keyunit") == 0);
    const GstSeekFlags sflags = (GstSeekFlags)(GST_SEEK_FLAG_FLUSH |
        (keyunit ? GST_SEEK_FLAG_KEY_UNIT : GST_SEEK_FLAG_ACCURATE));
    const int warm = 20;

    gst_init(&argc, &argv);

    int N = argc - 3;
    if (N > MAXN) N = MAXN;
    GstElement *pipe[MAXN];
    GstBus *bus[MAXN];
    for (int k = 0; k < N; k++) {
        fwdslash(argv[3 + k]);
        char s[2048];
        snprintf(s, sizeof s,
            "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! fakesink name=s sync=false",
            argv[3 + k]);
        GError *e = NULL;
        pipe[k] = gst_parse_launch(s, &e);
        if (!pipe[k] || e) { fprintf(stderr, "pipe %d parse failed: %s\n", k, e ? e->message : "?"); return 3; }
        bus[k] = gst_element_get_bus(pipe[k]);
        gst_element_set_state(pipe[k], GST_STATE_PAUSED);
    }
    for (int k = 0; k < N; k++)
        if (gst_element_get_state(pipe[k], NULL, NULL, 15 * GST_SECOND) != GST_STATE_CHANGE_SUCCESS) {
            fprintf(stderr, "preroll pipe %d failed/timeout\n", k); return 4;
        }

    const GstClockTime dur = 115 * GST_SECOND;
    double *ms = (double *)malloc(sizeof(double) * frames);
    int got = 0;
    for (int i = -warm; i < frames; i++) {
        int idx = i < 0 ? 0 : i;
        GstClockTime pos = (GstClockTime)((gdouble)dur * idx / frames);
        gint64 t0 = g_get_monotonic_time();
        for (int k = 0; k < N; k++)                       // fire all seeks -> decoders work in parallel
            gst_element_seek_simple(pipe[k], GST_FORMAT_TIME, sflags, (gint64)pos);
        int ok = 1;
        for (int k = 0; k < N; k++) {                     // collect all prerolls -> last one gates
            GstMessage *m = gst_bus_timed_pop_filtered(bus[k], 5 * GST_SECOND,
                (GstMessageType)(GST_MESSAGE_ASYNC_DONE | GST_MESSAGE_ERROR));
            if (!m) { fprintf(stderr, "timeout pipe %d i=%d\n", k, i); ok = 0; break; }
            if (GST_MESSAGE_TYPE(m) == GST_MESSAGE_ERROR) {
                GError *e = NULL; gst_message_parse_error(m, &e, NULL);
                fprintf(stderr, "pipe %d ERROR: %s\n", k, e ? e->message : "?");
                if (e) g_error_free(e); gst_message_unref(m); ok = 0; break;
            }
            gst_message_unref(m);
        }
        gint64 t1 = g_get_monotonic_time();
        if (!ok) break;
        if (i >= 0) ms[got++] = (double)(t1 - t0) / 1000.0;
    }
    for (int k = 0; k < N; k++) { gst_element_set_state(pipe[k], GST_STATE_NULL); gst_object_unref(bus[k]); gst_object_unref(pipe[k]); }

    if (got > 0) {
        qsort(ms, got, sizeof(double), cmp_double);
        double sum = 0; for (int i = 0; i < got; i++) sum += ms[i];
        double avg = sum / got, p99 = ms[(int)(0.99 * (got - 1))], mx = ms[got - 1];
        printf("{\"engine\":\"indep-d3d11\",\"mode\":\"%s\",\"layers\":%d,\"seeks\":%d,\"avg_ms\":%.3f,\"p99_ms\":%.3f,\"max_ms\":%.3f,\"fps\":%.1f}\n",
               keyunit ? "keyunit" : "accurate", N, got, avg, p99, mx, 1000.0 / avg);
        fprintf(stderr, "[indep-d3d11] %d-layer %s scrub: %.1f fps (%.2f ms avg, %.2f p99, %.2f max) over %d seeks\n",
                N, keyunit ? "keyunit" : "accurate", 1000.0 / avg, avg, p99, mx, got);
    } else {
        printf("{\"engine\":\"indep-d3d11\",\"error\":\"no timed seeks\"}\n");
    }
    free(ms);
    return 0;
}
