//go:build audio

/* native_bridge.c — pure-C audio I/O behind the becky_* ABI (native_bridge.h).
 *
 * Why this exists in C and not Go (SPEC §1.1, §6.2; CLAUDE.md §2): the miniaudio
 * data callback runs on the realtime audio thread. Go code there would be exposed
 * to GC stop-the-world pauses and you would hear the glitch. So the callbacks
 * here are PURE C and touch only C-allocated state; Go calls only the blocking
 * control-plane functions (enumerate/record/play), each on a goroutine.
 *
 * Borrowed approach: the record path uses miniaudio's lock-free PCM ring buffer
 * (ma_pcm_rb) as an always-on pre-roll, ported from dawbase/src/capture.cpp —
 * the callback writes into the ring and the control thread drains it, so a slow
 * disk write never stalls the audio thread.
 *
 * Callers: native_audio.go (//go:build audio) calls becky_enumerate,
 * becky_enumerate_free, becky_record_wav, becky_play_wav via cgo. miniaudio's
 * implementation is compiled separately in miniaudio_impl.c; here we include the
 * header for declarations only (no MINIAUDIO_IMPLEMENTATION).
 */
#include "native_bridge.h"

#include <math.h>
#include <stdlib.h>
#include <string.h>

#include "miniaudio.h"

/* becky_sleep_ms is a tiny portable millisecond sleep for the blocking control
 * threads (record/play/stream loops). miniaudio's own ma_sleep is a static inline
 * defined only inside MINIAUDIO_IMPLEMENTATION (compiled in miniaudio_impl.c), so
 * it is not visible in this translation unit — hence our own. */
#if defined(_WIN32)
#include <windows.h>
static void becky_sleep_ms(unsigned int ms) { Sleep(ms); }
#else
#include <time.h>
static void becky_sleep_ms(unsigned int ms) {
    struct timespec ts;
    ts.tv_sec = ms / 1000u;
    ts.tv_nsec = (long)(ms % 1000u) * 1000000L;
    nanosleep(&ts, NULL);
}
#endif

/* ---- enumeration ----------------------------------------------------------
 * We keep the real ma_device_id values in a C-side table so a later open can map
 * the opaque idIndex back to a device id without leaking miniaudio types to Go.
 * Indices are assigned playback-first then capture, matching becky_enumerate's
 * output order. */
static ma_device_id* g_idTable = NULL;
static int*          g_idIsCapture = NULL;
static int           g_idCount = 0;

void becky_enumerate_free(void) {
    free(g_idTable);
    free(g_idIsCapture);
    g_idTable = NULL;
    g_idIsCapture = NULL;
    g_idCount = 0;
}

/* copyName null-terminates an OS device name into the fixed dst buffer. */
static void copyName(char* dst, size_t dstSize, const char* src) {
    if (dstSize == 0) return;
    size_t n = 0;
    while (src[n] != '\0' && n + 1 < dstSize) {
        dst[n] = src[n];
        ++n;
    }
    dst[n] = '\0';
}

int becky_enumerate(becky_device_info* out, int maxOut, int* outCount) {
    if (outCount) *outCount = 0;
    if (!out || maxOut <= 0) return MA_INVALID_ARGS;

    ma_context ctx;
    if (ma_context_init(NULL, 0, NULL, &ctx) != MA_SUCCESS) {
        return MA_FAILED_TO_INIT_BACKEND;
    }

    ma_device_info* pPlayback = NULL;
    ma_uint32 playbackCount = 0;
    ma_device_info* pCapture = NULL;
    ma_uint32 captureCount = 0;
    ma_result r = ma_context_get_devices(&ctx, &pPlayback, &playbackCount,
                                         &pCapture, &captureCount);
    if (r != MA_SUCCESS) {
        ma_context_uninit(&ctx);
        return r;
    }

    /* (Re)build the id table so idIndex stays valid until the next enumerate. */
    becky_enumerate_free();
    int total = (int)playbackCount + (int)captureCount;
    if (total > 0) {
        g_idTable = (ma_device_id*)calloc((size_t)total, sizeof(ma_device_id));
        g_idIsCapture = (int*)calloc((size_t)total, sizeof(int));
        if (!g_idTable || !g_idIsCapture) {
            becky_enumerate_free();
            ma_context_uninit(&ctx);
            return MA_OUT_OF_MEMORY;
        }
    }

    int n = 0;
    for (ma_uint32 i = 0; i < playbackCount && n < maxOut; ++i, ++n) {
        becky_device_info* d = &out[n];
        copyName(d->name, sizeof(d->name), pPlayback[i].name);
        d->isCapture = 0;
        d->isDefault = pPlayback[i].isDefault ? 1 : 0;
        d->channels = pPlayback[i].nativeDataFormatCount > 0
                          ? (int)pPlayback[i].nativeDataFormats[0].channels
                          : 0;
        d->sampleRate = pPlayback[i].nativeDataFormatCount > 0
                            ? (int)pPlayback[i].nativeDataFormats[0].sampleRate
                            : 0;
        d->idIndex = g_idCount;
        g_idTable[g_idCount] = pPlayback[i].id;
        g_idIsCapture[g_idCount] = 0;
        ++g_idCount;
    }
    for (ma_uint32 i = 0; i < captureCount && n < maxOut; ++i, ++n) {
        becky_device_info* d = &out[n];
        copyName(d->name, sizeof(d->name), pCapture[i].name);
        d->isCapture = 1;
        d->isDefault = pCapture[i].isDefault ? 1 : 0;
        d->channels = pCapture[i].nativeDataFormatCount > 0
                          ? (int)pCapture[i].nativeDataFormats[0].channels
                          : 0;
        d->sampleRate = pCapture[i].nativeDataFormatCount > 0
                            ? (int)pCapture[i].nativeDataFormats[0].sampleRate
                            : 0;
        d->idIndex = g_idCount;
        g_idTable[g_idCount] = pCapture[i].id;
        g_idIsCapture[g_idCount] = 1;
        ++g_idCount;
    }

    if (outCount) *outCount = n;
    ma_context_uninit(&ctx);
    return MA_SUCCESS;
}

/* resolveDeviceID maps an idIndex from a prior enumerate to its ma_device_id,
 * checking the capture/playback direction matches. Returns NULL for -1 (use the
 * OS default) or an out-of-range / wrong-direction index (also OS default). */
static const ma_device_id* resolveDeviceID(int idIndex, int wantCapture) {
    if (idIndex < 0 || idIndex >= g_idCount || g_idTable == NULL) return NULL;
    if (g_idIsCapture[idIndex] != wantCapture) return NULL;
    return &g_idTable[idIndex];
}

/* ---- capture (record to WAV) ----------------------------------------------
 * The pre-roll ring buffer + draining encoder, ported from dawbase. The data
 * callback only writes into the ring (pure C, no alloc, no Go); the control
 * thread drains the ring into the WAV encoder on its own goroutine. */

typedef struct {
    ma_pcm_rb rb;
    int       channels;
    int       overflow; /* set if the ring filled and oldest frames were dropped */
} CaptureCtx;

static void captureCallback(ma_device* dev, void* pOutput, const void* pInput,
                            ma_uint32 frameCount) {
    (void)pOutput;
    CaptureCtx* c = (CaptureCtx*)dev->pUserData;
    const float* src = (const float*)pInput;
    ma_uint32 left = frameCount;
    while (left > 0) {
        ma_uint32 want = left;
        void* dst = NULL;
        if (ma_pcm_rb_acquire_write(&c->rb, &want, &dst) != MA_SUCCESS ||
            want == 0) {
            /* ring full: drop oldest to keep the most-recent audio (pre-roll). */
            c->overflow = 1;
            ma_pcm_rb_seek_read(&c->rb, left);
            continue;
        }
        memcpy(dst, src, (size_t)want * (size_t)c->channels * sizeof(float));
        ma_pcm_rb_commit_write(&c->rb, want);
        src += (size_t)want * (size_t)c->channels;
        left -= want;
    }
}

/* drainAndEncode pulls everything currently readable out of the ring and writes
 * it to the encoder. Runs on the control thread, not the audio thread. */
static ma_result drainAndEncode(CaptureCtx* c, ma_encoder* enc) {
    ma_uint32 avail = ma_pcm_rb_available_read(&c->rb);
    ma_uint32 got = 0;
    while (got < avail) {
        ma_uint32 n = avail - got;
        void* p = NULL;
        if (ma_pcm_rb_acquire_read(&c->rb, &n, &p) != MA_SUCCESS || n == 0) {
            break;
        }
        ma_uint64 written = 0;
        ma_encoder_write_pcm_frames(enc, p, n, &written);
        ma_pcm_rb_commit_read(&c->rb, n);
        got += n;
    }
    return MA_SUCCESS;
}

int becky_record_wav(int captureIdIndex, const char* path, double seconds,
                     int sampleRate, int channels) {
    if (!path) return MA_INVALID_ARGS;
    if (seconds <= 0.0) seconds = 1.0;
    if (sampleRate <= 0) sampleRate = 48000;
    if (channels <= 0) channels = 1;

    CaptureCtx cap;
    memset(&cap, 0, sizeof(cap));
    cap.channels = channels;

    /* Ring sized to ~1 second of pre-roll; the drain loop empties it ~20x/sec so
     * it never overflows under normal disk speeds, and degrades by dropping the
     * oldest frames if it ever does. */
    ma_uint32 ringFrames = (ma_uint32)sampleRate;
    if (ringFrames < 1024) ringFrames = 1024;
    if (ma_pcm_rb_init(ma_format_f32, (ma_uint32)channels, ringFrames, NULL,
                       NULL, &cap.rb) != MA_SUCCESS) {
        return MA_OUT_OF_MEMORY;
    }

    /* Open the WAV encoder first so a bad path fails before we touch hardware. */
    ma_encoder_config encCfg = ma_encoder_config_init(
        ma_encoding_format_wav, ma_format_f32, (ma_uint32)channels,
        (ma_uint32)sampleRate);
    ma_encoder enc;
    if (ma_encoder_init_file(path, &encCfg, &enc) != MA_SUCCESS) {
        ma_pcm_rb_uninit(&cap.rb);
        return MA_INVALID_FILE; /* path not writable / bad path */
    }

    ma_device_config cfg = ma_device_config_init(ma_device_type_capture);
    cfg.capture.format = ma_format_f32;
    cfg.capture.channels = (ma_uint32)channels;
    cfg.capture.pDeviceID = (ma_device_id*)resolveDeviceID(captureIdIndex, 1);
    cfg.sampleRate = (ma_uint32)sampleRate;
    cfg.performanceProfile = ma_performance_profile_low_latency;
    cfg.pUserData = &cap;
    cfg.dataCallback = captureCallback;

    ma_device dev;
    if (ma_device_init(NULL, &cfg, &dev) != MA_SUCCESS) {
        ma_encoder_uninit(&enc);
        ma_pcm_rb_uninit(&cap.rb);
        return MA_NO_DEVICE;
    }
    if (ma_device_start(&dev) != MA_SUCCESS) {
        ma_device_uninit(&dev);
        ma_encoder_uninit(&enc);
        ma_pcm_rb_uninit(&cap.rb);
        return MA_FAILED_TO_START_BACKEND_DEVICE;
    }

    /* Record for `seconds`, draining the ring ~20x/sec so the encoder keeps up
     * and the ring never has to drop frames. ma_sleep is a portable C sleep. */
    int ticks = (int)(seconds * 20.0);
    if (ticks < 1) ticks = 1;
    for (int i = 0; i < ticks; ++i) {
        becky_sleep_ms(50);
        drainAndEncode(&cap, &enc);
    }

    ma_device_uninit(&dev); /* stops the callback first */
    drainAndEncode(&cap, &enc); /* flush any frames left in the ring */

    ma_encoder_uninit(&enc);
    ma_pcm_rb_uninit(&cap.rb);
    return MA_SUCCESS;
}

/* ---- playback (play a WAV) -------------------------------------------------
 * The data callback reads decoded frames straight from the high-level decoder
 * (pure C) and signals "done" via an atomic flag the control thread polls. */

typedef struct {
    ma_decoder    decoder;
    volatile int  finished; /* set to 1 by the callback at end of file.
                             * Single writer (audio thread) / single reader
                             * (control thread) done-flag — volatile is the
                             * minimal correct primitive and avoids depending on
                             * miniaudio's internal atomic type surface. */
} PlaybackCtx;

static void playbackCallback(ma_device* dev, void* pOutput, const void* pInput,
                             ma_uint32 frameCount) {
    (void)pInput;
    PlaybackCtx* p = (PlaybackCtx*)dev->pUserData;
    ma_uint64 framesRead = 0;
    ma_result r = ma_decoder_read_pcm_frames(&p->decoder, pOutput, frameCount,
                                             &framesRead);
    /* If we hit the end (or an error), flag done; miniaudio pre-silences the
     * unread tail of pOutput for us, so partial last buffers play cleanly. */
    if (r != MA_SUCCESS || framesRead < frameCount) {
        p->finished = 1;
    }
}

int becky_play_wav(int playbackIdIndex, const char* path) {
    if (!path) return MA_INVALID_ARGS;

    PlaybackCtx pb;
    memset(&pb, 0, sizeof(pb)); /* finished = 0 */

    /* Decode keeping the file's own channels/rate (0,0); request f32 frames so
     * the device format matches the callback's writes. */
    ma_decoder_config decCfg = ma_decoder_config_init(ma_format_f32, 0, 0);
    if (ma_decoder_init_file(path, &decCfg, &pb.decoder) != MA_SUCCESS) {
        return MA_INVALID_FILE; /* file missing / not a WAV */
    }

    ma_device_config cfg = ma_device_config_init(ma_device_type_playback);
    cfg.playback.format = pb.decoder.outputFormat;
    cfg.playback.channels = pb.decoder.outputChannels;
    cfg.playback.pDeviceID = (ma_device_id*)resolveDeviceID(playbackIdIndex, 0);
    cfg.sampleRate = pb.decoder.outputSampleRate;
    cfg.performanceProfile = ma_performance_profile_low_latency;
    cfg.pUserData = &pb;
    cfg.dataCallback = playbackCallback;

    ma_device dev;
    if (ma_device_init(NULL, &cfg, &dev) != MA_SUCCESS) {
        ma_decoder_uninit(&pb.decoder);
        return MA_NO_DEVICE;
    }
    if (ma_device_start(&dev) != MA_SUCCESS) {
        ma_device_uninit(&dev);
        ma_decoder_uninit(&pb.decoder);
        return MA_FAILED_TO_START_BACKEND_DEVICE;
    }

    /* Block on the control thread until the callback reports end-of-file. */
    while (pb.finished == 0) {
        becky_sleep_ms(20);
    }

    ma_device_uninit(&dev);
    ma_decoder_uninit(&pb.decoder);
    return MA_SUCCESS;
}

/* ---- running output stream (AudioBackend.Start / Stop) --------------------
 * One process-wide engine output device. The callback emits silence today; the
 * realtime mixer that walks the compiled graph (SPEC §4) replaces the body
 * later. Kept as a singleton because there is one engine output at a time. */

static ma_device g_streamDev;
static int       g_streamRunning = 0;

static void silenceCallback(ma_device* dev, void* pOutput, const void* pInput,
                            ma_uint32 frameCount) {
    (void)dev;
    (void)pInput;
    /* Pure C, no alloc, no Go: write silence. The format is f32, so zeroing the
     * buffer is silence. The real mixer fills this from the graph schedule. */
    ma_uint32 channels = dev->playback.channels;
    memset(pOutput, 0, (size_t)frameCount * (size_t)channels * sizeof(float));
}

int becky_stream_start(int playbackIdIndex, int sampleRate, int channels) {
    if (g_streamRunning) return MA_SUCCESS; /* idempotent */
    if (sampleRate <= 0) sampleRate = 48000;
    if (channels <= 0) channels = 2;

    ma_device_config cfg = ma_device_config_init(ma_device_type_playback);
    cfg.playback.format = ma_format_f32;
    cfg.playback.channels = (ma_uint32)channels;
    cfg.playback.pDeviceID = (ma_device_id*)resolveDeviceID(playbackIdIndex, 0);
    cfg.sampleRate = (ma_uint32)sampleRate;
    cfg.performanceProfile = ma_performance_profile_low_latency;
    cfg.dataCallback = silenceCallback;

    if (ma_device_init(NULL, &cfg, &g_streamDev) != MA_SUCCESS) {
        return MA_NO_DEVICE;
    }
    if (ma_device_start(&g_streamDev) != MA_SUCCESS) {
        ma_device_uninit(&g_streamDev);
        return MA_FAILED_TO_START_BACKEND_DEVICE;
    }
    g_streamRunning = 1;
    return MA_SUCCESS;
}

void becky_stream_stop(void) {
    if (!g_streamRunning) return; /* safe no-op when stopped */
    ma_device_uninit(&g_streamDev);
    g_streamRunning = 0;
}
