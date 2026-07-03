// ges_scrub.c - measure 2-layer (PiP) SCRUB latency in GStreamer Editing Services on Windows.
//
// The GES twin of native/timeline-bench (which proved 2x libmpv collapses to ~20 fps with 500 ms
// stalls). Builds a 2-layer video timeline (fileA full-frame on layer 0; fileB half-alpha on
// layer 1 so the compositor MUST blend both, no occlusion cull), then hammers FLUSH+ACCURATE
// seeks across it and times each preroll. This is exactly a scrub: every seek is a frame-accurate
// jump the user would make by dragging the playhead. d3d11 GPU decode is forced by the launcher
// via GST_PLUGIN_FEATURE_RANK. Headless: renders to fakesink (still decodes + composites).
//
//   usage: ges_scrub.exe <fileA> <fileB> [frames]
#include <gst/gst.h>
#include <ges/ges.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int cmp_double(const void *a, const void *b) {
    double d = *(const double *)a - *(const double *)b;
    return (d > 0) - (d < 0);
}

int main(int argc, char **argv) {
    if (argc < 3) { fprintf(stderr, "usage: ges_scrub <fileA> <fileB> [frames]\n"); return 2; }
    const int frames = (argc > 3) ? atoi(argv[3]) : 200;
    const int warm = 20;
    // ACCURATE = decode from the previous keyframe (expensive on long-GOP raw footage);
    // KEY_UNIT = snap to the nearest keyframe (~one decode) -> isolates ENGINE cost from
    // decode cost, the same way the libmpv bench used cheap seeks. Pass "keyunit" to switch.
    const int keyunit = (argc > 4 && strcmp(argv[4], "keyunit") == 0);
    const GstSeekFlags sflags = (GstSeekFlags)(GST_SEEK_FLAG_FLUSH |
        (keyunit ? GST_SEEK_FLAG_KEY_UNIT : GST_SEEK_FLAG_ACCURATE));

    gst_init(&argc, &argv);
    ges_init();

    gchar *uriA = gst_filename_to_uri(argv[1], NULL);
    gchar *uriB = gst_filename_to_uri(argv[2], NULL);
    if (!uriA || !uriB) { fprintf(stderr, "bad path -> uri\n"); return 2; }

    GESTimeline *timeline = ges_timeline_new();
    ges_timeline_add_track(timeline, GES_TRACK(ges_video_track_new()));
    GESLayer *layer0 = ges_timeline_append_layer(timeline);  // bottom / main
    GESLayer *layer1 = ges_timeline_append_layer(timeline);  // top / PiP

    const GstClockTime inpoint = 60 * GST_SECOND;
    const GstClockTime dur     = 600 * GST_SECOND;  // 10 min scrub range, inside both files

    GESUriClip *clipA = ges_uri_clip_new(uriA);
    GESUriClip *clipB = ges_uri_clip_new(uriB);
    if (!clipA || !clipB) { fprintf(stderr, "GES asset load failed\n"); return 3; }
    ges_timeline_element_set_start   (GES_TIMELINE_ELEMENT(clipA), 0);
    ges_timeline_element_set_inpoint (GES_TIMELINE_ELEMENT(clipA), inpoint);
    ges_timeline_element_set_duration(GES_TIMELINE_ELEMENT(clipA), dur);
    ges_timeline_element_set_start   (GES_TIMELINE_ELEMENT(clipB), 0);
    ges_timeline_element_set_inpoint (GES_TIMELINE_ELEMENT(clipB), inpoint);
    ges_timeline_element_set_duration(GES_TIMELINE_ELEMENT(clipB), dur);
    ges_layer_add_clip(layer0, GES_CLIP(clipA));
    ges_layer_add_clip(layer1, GES_CLIP(clipB));

    // Half-alpha on the top layer forces the compositor to blend BOTH layers every frame
    // (an opaque top layer could be occlusion-culled -> would fake a 1-layer number).
    GValue a = G_VALUE_INIT;
    g_value_init(&a, G_TYPE_DOUBLE);
    g_value_set_double(&a, 0.5);
    if (!ges_timeline_element_set_child_property(GES_TIMELINE_ELEMENT(clipB), "alpha", &a))
        fprintf(stderr, "warn: could not set 'alpha' child prop (top layer may occlude)\n");
    g_value_unset(&a);

    ges_timeline_commit(timeline);

    GESPipeline *pipeline = ges_pipeline_new();
    ges_pipeline_set_timeline(pipeline, timeline);
    ges_pipeline_set_mode(pipeline, GES_PIPELINE_MODE_PREVIEW_VIDEO);
    GstElement *vsink = gst_element_factory_make("fakesink", "vsink");
    g_object_set(vsink, "sync", FALSE, "async", FALSE, NULL);
    ges_pipeline_preview_set_video_sink(pipeline, vsink);

    GstElement *pel = GST_ELEMENT(pipeline);
    GstBus *bus = gst_element_get_bus(pel);
    if (gst_element_set_state(pel, GST_STATE_PAUSED) == GST_STATE_CHANGE_FAILURE) {
        fprintf(stderr, "set PAUSED failed\n"); return 4;
    }
    if (gst_element_get_state(pel, NULL, NULL, 15 * GST_SECOND) != GST_STATE_CHANGE_SUCCESS) {
        fprintf(stderr, "preroll to PAUSED failed/timeout\n"); return 4;
    }

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
        if (i >= 0) ms[got++] = (double)(t1 - t0) / 1000.0;  // us -> ms
    }
    gst_element_set_state(pel, GST_STATE_NULL);

    if (got > 0) {
        qsort(ms, got, sizeof(double), cmp_double);
        double sum = 0; for (int i = 0; i < got; i++) sum += ms[i];
        double avg = sum / got, p99 = ms[(int)(0.99 * (got - 1))], mx = ms[got - 1];
        printf("{\"engine\":\"ges\",\"mode\":\"%s\",\"layers\":2,\"seeks\":%d,\"avg_ms\":%.3f,\"p99_ms\":%.3f,\"max_ms\":%.3f,\"fps\":%.1f}\n",
               keyunit ? "keyunit" : "accurate", got, avg, p99, mx, 1000.0 / avg);
        fprintf(stderr, "[ges] 2-layer %s scrub: %.1f fps (%.2f ms avg, %.2f p99, %.2f max) over %d seeks\n",
                keyunit ? "keyunit" : "accurate", 1000.0 / avg, avg, p99, mx, got);
    } else {
        printf("{\"engine\":\"ges\",\"error\":\"no timed seeks\"}\n");
    }
    free(ms);
    gst_object_unref(bus);
    gst_object_unref(pipeline);
    return 0;
}
