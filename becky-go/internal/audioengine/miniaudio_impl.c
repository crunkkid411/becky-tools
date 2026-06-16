//go:build audio

/* miniaudio_impl.c — the single translation unit that compiles miniaudio.
 *
 * miniaudio is a single-header library: every other file includes "miniaudio.h"
 * as a normal header, and exactly ONE source file defines
 * MINIAUDIO_IMPLEMENTATION before including it to pull in the implementation.
 * That file is this one. Built only under `//go:build audio` (see the cgo
 * preamble in native_audio.go), so the default pure-Go build never compiles it.
 *
 * License: miniaudio is public-domain / MIT-0 (vendored, see miniaudio.h tail).
 *
 * Build trims (Windows-first, keep the binary lean and the build fast):
 *  - Only the WASAPI backend is enabled (this is a Win10 target; SPEC §1.2).
 *  - Decoding/encoding limited to WAV (dr_wav path) — that is all record/play
 *    needs and it avoids pulling MP3/FLAC/Vorbis decoders we do not use.
 */

/* Enable only the WASAPI backend (Windows). Other backends are disabled so the
 * implementation stays small and the WASAPI device-default rule is exercised. */
#define MA_ENABLE_ONLY_SPECIFIC_BACKENDS
#define MA_ENABLE_WASAPI

/* We only need WAV for the becky record/play path. Disable the other decoders
 * and encoders to keep the translation unit small. */
#define MA_NO_FLAC
#define MA_NO_MP3
#define MA_NO_VORBIS

#define MINIAUDIO_IMPLEMENTATION
#include "miniaudio.h"
