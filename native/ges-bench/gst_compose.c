// gst_compose.c - PROOF of the production hot path: two independently-seeked d3d11 GPU decoders
// composed into ONE frame. Decode (GStreamer d3d11, proven 2325 fps in gst_scrub_indep) -> seek
// each layer to the same time -> pull both frames -> composite (layer A full, layer B as a
// half-size top-right PiP at alpha 0.5 so BOTH are visible) -> write RGBA. A PNG made from the
// output is the self-check: two distinct real frames, correctly composited.
//
// CPU composite here (d3d11download -> system memory) deliberately: it sidesteps shared-GPU-device
// plumbing (a production perf detail; GPU-compositor speed is already implied by the 2325 fps
// decode + a trivial shader). This proves CORRECTNESS of the layered hot path, not its ceiling.
//
//   usage: gst_compose.exe <fileA> <fileB> <out.rgba> [seconds]
#include <gst/gst.h>
#include <gst/app/gstappsink.h>
#include <gst/video/video.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static void fwdslash(char *s) { for (; *s; ++s) if (*s == '\\') *s = '/'; }

// Pull the frame at `pos` from one decode pipeline. Returns a mapped GstVideoFrame (caller unmaps)
// + its sample (caller unrefs). 1 on success.
static int seek_and_pull(GstElement *pipe, GstBus *bus, GstClockTime pos,
                         GstVideoFrame *frame, GstSample **sample_out) {
    gst_element_seek_simple(pipe, GST_FORMAT_TIME,
        (GstSeekFlags)(GST_SEEK_FLAG_FLUSH | GST_SEEK_FLAG_ACCURATE), (gint64)pos);
    GstMessage *m = gst_bus_timed_pop_filtered(bus, 5 * GST_SECOND,
        (GstMessageType)(GST_MESSAGE_ASYNC_DONE | GST_MESSAGE_ERROR));
    if (!m) { fprintf(stderr, "seek: no ASYNC_DONE\n"); return 0; }
    int err = (GST_MESSAGE_TYPE(m) == GST_MESSAGE_ERROR);
    gst_message_unref(m);
    if (err) { fprintf(stderr, "seek: pipeline error\n"); return 0; }

    GstElement *sink = gst_bin_get_by_name(GST_BIN(pipe), "s");
    GstSample *s = gst_app_sink_pull_preroll(GST_APP_SINK(sink));
    gst_object_unref(sink);
    if (!s) { fprintf(stderr, "pull_preroll returned NULL\n"); return 0; }

    GstCaps *caps = gst_sample_get_caps(s);
    GstVideoInfo info;
    if (!gst_video_info_from_caps(&info, caps)) { fprintf(stderr, "video_info_from_caps failed\n"); gst_sample_unref(s); return 0; }
    GstBuffer *buf = gst_sample_get_buffer(s);
    if (!gst_video_frame_map(frame, &info, buf, GST_MAP_READ)) { fprintf(stderr, "video_frame_map failed\n"); gst_sample_unref(s); return 0; }
    *sample_out = s;
    return 1;
}

int main(int argc, char **argv) {
    if (argc < 4) { fprintf(stderr, "usage: gst_compose <fileA> <fileB> <out.rgba> [seconds]\n"); return 2; }
    fwdslash(argv[1]); fwdslash(argv[2]);
    const char *outpath = argv[3];
    const double seconds = (argc > 4) ? atof(argv[4]) : 60.0;

    gst_init(&argc, &argv);

    const char *tmpl =
        "filesrc location=\"%s\" ! parsebin ! d3d11h264dec ! d3d11convert ! "
        "video/x-raw(memory:D3D11Memory),format=RGBA ! d3d11download ! "
        "appsink name=s sync=false max-buffers=2";
    GstElement *pipe[2]; GstBus *bus[2];
    for (int k = 0; k < 2; k++) {
        char s[2048]; snprintf(s, sizeof s, tmpl, argv[1 + k]);
        GError *e = NULL; pipe[k] = gst_parse_launch(s, &e);
        if (!pipe[k] || e) { fprintf(stderr, "parse %d failed: %s\n", k, e ? e->message : "?"); return 3; }
        bus[k] = gst_element_get_bus(pipe[k]);
        gst_element_set_state(pipe[k], GST_STATE_PAUSED);
    }
    for (int k = 0; k < 2; k++)
        if (gst_element_get_state(pipe[k], NULL, NULL, 15 * GST_SECOND) != GST_STATE_CHANGE_SUCCESS) {
            fprintf(stderr, "preroll %d failed\n", k); return 4;
        }

    GstClockTime pos = (GstClockTime)(seconds * GST_SECOND);
    GstVideoFrame fa, fb; GstSample *sa = NULL, *sb = NULL;
    if (!seek_and_pull(pipe[0], bus[0], pos, &fa, &sa)) return 5;
    if (!seek_and_pull(pipe[1], bus[1], pos, &fb, &sb)) return 5;

    int wa = GST_VIDEO_FRAME_WIDTH(&fa),  ha = GST_VIDEO_FRAME_HEIGHT(&fa);
    int wb = GST_VIDEO_FRAME_WIDTH(&fb),  hb = GST_VIDEO_FRAME_HEIGHT(&fb);
    guint8 *da = (guint8 *)GST_VIDEO_FRAME_PLANE_DATA(&fa, 0); int sta = GST_VIDEO_FRAME_PLANE_STRIDE(&fa, 0);
    guint8 *db = (guint8 *)GST_VIDEO_FRAME_PLANE_DATA(&fb, 0); int stb = GST_VIDEO_FRAME_PLANE_STRIDE(&fb, 0);
    fprintf(stderr, "layer A %dx%d stride %d | layer B %dx%d stride %d\n", wa, ha, sta, wb, hb, stb);

    // Output = A's size, RGBA tightly packed.
    guint8 *out = (guint8 *)malloc((size_t)wa * ha * 4);
    for (int y = 0; y < ha; y++)
        memcpy(out + (size_t)y * wa * 4, da + (size_t)y * sta, (size_t)wa * 4);

    // PiP: layer B at half size in the top-right, alpha 0.5 (both visible = real composite proof).
    int pw = wb / 2, ph = hb / 2, ox = wa - pw - wa / 20, oy = ha / 20;
    for (int y = 0; y < ph; y++) {
        int dy = oy + y; if (dy < 0 || dy >= ha) continue;
        for (int x = 0; x < pw; x++) {
            int dx = ox + x; if (dx < 0 || dx >= wa) continue;
            const guint8 *src = db + (size_t)(y * 2) * stb + (size_t)(x * 2) * 4;  // nearest-neighbour 2x downscale
            guint8 *dst = out + ((size_t)dy * wa + dx) * 4;
            for (int c = 0; c < 3; c++) dst[c] = (guint8)((dst[c] + src[c]) / 2);  // alpha 0.5
        }
    }

    FILE *f = fopen(outpath, "wb");
    if (!f) { fprintf(stderr, "cannot open %s\n", outpath); return 6; }
    fwrite(out, 1, (size_t)wa * ha * 4, f);
    fclose(f);
    printf("{\"ok\":true,\"width\":%d,\"height\":%d,\"format\":\"rgba\",\"out\":\"%s\"}\n", wa, ha, outpath);

    free(out);
    gst_video_frame_unmap(&fa); gst_video_frame_unmap(&fb);
    gst_sample_unref(sa); gst_sample_unref(sb);
    for (int k = 0; k < 2; k++) { gst_element_set_state(pipe[k], GST_STATE_NULL); gst_object_unref(bus[k]); gst_object_unref(pipe[k]); }
    return 0;
}
