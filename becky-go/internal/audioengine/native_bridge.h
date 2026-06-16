/* native_bridge.h — the small C ABI cgo calls into for real audio I/O.
 *
 * Design rule (SPEC §1, §6.2; CLAUDE.md §2): the realtime audio callback is
 * PURE C and never enters the Go runtime, so a GC stop-the-world pause can never
 * land inside the audio deadline. Go only calls the control-plane functions
 * below (enumerate / start / stop / poll), each of which runs on a Go goroutine,
 * NOT on the audio thread. All audio-thread state lives in C structs allocated
 * here; nothing crosses into Go memory on the callback.
 *
 * Callers: native_bridge.c implements these; native_audio.go (//go:build audio)
 * includes this header in its cgo preamble and calls becky_enumerate,
 * becky_enumerate_free, becky_record_wav, becky_play_wav.
 *
 * Every function returns a miniaudio ma_result-style int (0 == MA_SUCCESS) or a
 * documented sentinel, so Go can degrade-never-crash on any failure instead of
 * panicking (no device, failed open, etc.).
 */
#ifndef BECKY_NATIVE_BRIDGE_H
#define BECKY_NATIVE_BRIDGE_H

#include <stddef.h>

/* ---- Device enumeration ----------------------------------------------------
 * One enumerated endpoint, flattened for cgo (no miniaudio types cross the
 * boundary — only this POD struct does, matching the Go Device shape). */
typedef struct {
    char name[256];     /* OS-reported device name (null-terminated). */
    int  isCapture;     /* 1 = capture (mic/line-in), 0 = playback. */
    int  isDefault;     /* 1 if the OS default endpoint for its direction. */
    int  channels;      /* native channel count (0 if unknown). */
    int  sampleRate;    /* native sample rate in Hz (0 if unknown). */
    int  idIndex;       /* opaque index into the C-side device-id table. */
} becky_device_info;

/* becky_enumerate fills up to maxOut becky_device_info entries (playback first,
 * then capture) and writes the count to *outCount. Returns MA_SUCCESS (0) or a
 * miniaudio error. The id table is retained internally so a later open can map
 * idIndex back to the real ma_device_id; call becky_enumerate_free when done. */
int becky_enumerate(becky_device_info* out, int maxOut, int* outCount);

/* Release the internal id table allocated by the last becky_enumerate. */
void becky_enumerate_free(void);

/* ---- Capture-to-WAV (record) ----------------------------------------------
 * becky_record_wav opens the chosen capture device (idIndex from a prior
 * enumerate, or -1 for the OS default), records into an always-on pre-roll ring
 * (the dawbase idea: nothing is clipped), and writes a float32 WAV.
 *
 *   captureIdIndex : device to open (-1 = default).
 *   path           : output .wav path (UTF-8). Caller owns the string.
 *   seconds        : how long to record (<= 0 falls back to 1s).
 *   sampleRate     : requested rate (e.g. 48000).
 *   channels       : requested channels (1 = mono, 2 = stereo).
 *
 * Blocks for `seconds`, then stops and writes the file. The audio callback that
 * fills the ring is pure C. Returns MA_SUCCESS (0) or a miniaudio error. */
int becky_record_wav(int captureIdIndex, const char* path,
                     double seconds, int sampleRate, int channels);

/* ---- Playback of a WAV file ------------------------------------------------
 * becky_play_wav opens the chosen playback device (idIndex, or -1 for default),
 * decodes the WAV with the high-level decoder, and streams it to the device via
 * a pure-C callback until the file is exhausted, then stops. Blocks until done.
 * Returns MA_SUCCESS (0), or a miniaudio error (e.g. file not found / no
 * device). */
int becky_play_wav(int playbackIdIndex, const char* path);

/* ---- Running output stream (AudioBackend.Start / Stop) ---------------------
 * becky_stream_start opens the chosen playback device (idIndex, or -1 default)
 * and begins a continuously-running stream whose pure-C callback currently emits
 * silence — the real-time mixer/graph (SPEC §4) is the deferred next step that
 * fills these buffers. This makes AudioBackend.Start a REAL open device with a
 * live callback (not a stub), and becky_stream_stop closes it cleanly.
 *
 * State is held in a single C-side singleton (one engine output at a time);
 * start when already started returns MA_SUCCESS (idempotent re-open is avoided),
 * and stop when stopped is a safe no-op. Returns MA_SUCCESS (0) or a miniaudio
 * error. */
int  becky_stream_start(int playbackIdIndex, int sampleRate, int channels);
void becky_stream_stop(void);

#endif /* BECKY_NATIVE_BRIDGE_H */
