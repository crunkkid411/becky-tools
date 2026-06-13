#!/usr/bin/env python3
"""becky Silero VAD helper via sherpa-onnx (no torch, no internet).

Reads a 16 kHz mono WAV and emits one line of JSON to stdout (or --output).

Default (segment post-pass) mode — powers becky-cut:

  {"sample_rate", "duration", "threshold",
   "segments": [{"start", "end"}],   # speech spans, seconds
   "frames": [bool, ...],            # per-video-frame speech flags (if --fps>0)
   "speech_pct"}                     # % of duration that is speech

Full-segments mode (--full-segments) — powers standalone becky-vad. Emits a
contiguous, alternating speech / non-speech timeline that spans the WHOLE file,
each segment carrying its own speech percentage:

  {"sample_rate", "duration", "threshold",
   "full_segments": [{"start", "end", "speech_pct", "is_speech"}],
   "speech_pct"}                     # % of duration that is speech

This is the spec's "Silero VAD (via sherpa-onnx)" path. The Go caller decides
the per-segment `keep` flag from speech_pct vs --min-speech-pct.

On failure it emits {"skipped": true, "reason": "..."} and exits 0.
"""
import argparse
import array
import json
import sys
import wave

SAMPLE_RATE = 16000


def run_vad(args):
    """Run Silero VAD over the WAV and return (sr, duration, speech_spans).

    speech_spans is a list of (start_sec, end_sec) speech regions.
    """
    import sherpa_onnx

    cfg = sherpa_onnx.VadModelConfig()
    cfg.silero_vad.model = args.model
    cfg.silero_vad.threshold = args.threshold
    cfg.silero_vad.min_silence_duration = args.min_silence
    cfg.silero_vad.min_speech_duration = args.min_speech
    cfg.sample_rate = SAMPLE_RATE

    with wave.open(args.audio, "rb") as wf:
        sr = wf.getframerate()
        raw = wf.readframes(wf.getnframes())
    samples_int = array.array("h", raw)
    samples = [s / 32768.0 for s in samples_int]
    duration = len(samples) / float(sr) if sr else 0.0

    vad = sherpa_onnx.VoiceActivityDetector(
        cfg, buffer_size_in_seconds=max(1, duration + 5)
    )

    window = 512
    spans = []

    def drain():
        while not vad.empty():
            seg = vad.front
            start = seg.start / float(SAMPLE_RATE)
            end = (seg.start + len(seg.samples)) / float(SAMPLE_RATE)
            spans.append((start, end))
            vad.pop()

    i = 0
    while i < len(samples):
        vad.accept_waveform(samples[i:i + window])
        i += window
        drain()
    vad.flush()
    drain()

    return sr, duration, spans


def clamp_spans(spans, duration):
    """Sort, clamp to [0, duration], and merge overlapping speech spans."""
    cleaned = []
    for start, end in spans:
        start = max(0.0, min(start, duration))
        end = max(0.0, min(end, duration))
        if end > start:
            cleaned.append((start, end))
    cleaned.sort()
    merged = []
    for start, end in cleaned:
        if merged and start <= merged[-1][1] + 1e-6:
            merged[-1] = (merged[-1][0], max(merged[-1][1], end))
        else:
            merged.append((start, end))
    return merged


def build_full_segments(spans, duration):
    """Build a contiguous alternating speech / non-speech timeline.

    Returns a list of dicts spanning [0, duration] with no gaps or overlaps.
    Each segment's speech_pct is 100 for a speech span and 0 for a silence gap;
    the Go caller compares this against --min-speech-pct to set `keep`.
    """
    merged = clamp_spans(spans, duration)
    segs = []
    cursor = 0.0
    for start, end in merged:
        if start > cursor + 1e-6:
            segs.append((cursor, start, False))
        segs.append((start, end, True))
        cursor = end
    if duration > cursor + 1e-6:
        segs.append((cursor, duration, False))
    if not segs and duration > 0:
        segs.append((0.0, duration, False))

    out = []
    for start, end, is_speech in segs:
        speech_pct = 100.0 if is_speech else 0.0
        out.append({
            "start": round(start, 3),
            "end": round(end, 3),
            "speech_pct": round(speech_pct, 2),
            "is_speech": is_speech,
        })
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--model", required=True)
    ap.add_argument("--threshold", type=float, default=0.5)
    ap.add_argument("--min-silence", type=float, default=0.1)
    ap.add_argument("--min-speech", type=float, default=0.1)
    ap.add_argument("--fps", type=float, default=0.0)  # >0 to emit per-frame flags
    ap.add_argument("--full-segments", action="store_true",
                    help="emit a whole-file alternating speech/non-speech timeline")
    ap.add_argument("--output", default="")
    args = ap.parse_args()

    sr, duration, spans = run_vad(args)

    speech_seconds = sum(e - s for s, e in clamp_spans(spans, duration))
    speech_pct = round(100.0 * speech_seconds / duration, 2) if duration > 0 else 0.0

    out = {
        "sample_rate": sr,
        "duration": round(duration, 3),
        "threshold": args.threshold,
        "speech_pct": speech_pct,
    }

    if args.full_segments:
        out["full_segments"] = build_full_segments(spans, duration)
    else:
        segments = [
            {"start": round(s, 3), "end": round(e, 3)}
            for s, e in clamp_spans(spans, duration)
        ]
        out["segments"] = segments
        if args.fps > 0:
            num_frames = max(1, int(round(duration * args.fps)))
            frames = [False] * num_frames
            for seg in segments:
                a = max(0, int(round(seg["start"] * args.fps)))
                b = min(num_frames, int(round(seg["end"] * args.fps)))
                for f in range(a, b):
                    frames[f] = True
            out["frames"] = frames

    payload = json.dumps(out)
    if args.output:
        with open(args.output, "w") as f:
            f.write(payload)
    else:
        print(payload)


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
