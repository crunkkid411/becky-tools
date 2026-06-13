#!/usr/bin/env python3
"""becky-diarize helper: speaker diarization (ONNX) via sherpa-onnx.

Thin binding over sherpa-onnx's C++/ONNX core. Reads a 16 kHz mono WAV and runs
`OfflineSpeakerDiarization` (pyannote segmentation + CAM++ embeddings + fast
clustering), then emits one line of JSON to stdout:

  {"duration", "sample_rate", "num_speakers",
   "segments": [{"start", "end", "speaker"}]}   # speaker = "SPEAKER_00", ...

Why this file is not naive sherpa diarization
---------------------------------------------
Raw `OfflineSpeakerDiarization` over-splits a single talker on real
social-media footage: background music / intro stings / SFX get embedded by
CAM++ as phantom speakers, and short utterances over noise land as tiny
outlier clusters that FastClustering never merges regardless of threshold. A
single-talker clip therefore came back as 5 speakers. Two fixes, applied only
in auto mode (num_clusters == -1) so explicit --min/--max pinning is untouched:

  1. VAD speech-gating (default ON): run Silero VAD, diarize ONLY the
     concatenated speech regions, then map timestamps back to the original
     timeline. This strips non-speech that pollutes embeddings.
  2. Outlier-speaker merge: after clustering, speakers whose total speech time
     is below --min-speaker-duration OR below --min-speaker-frac of all speech
     are reassigned to the nearest-in-time surviving speaker. This collapses
     the stubborn tiny singleton a high threshold cannot.

Together with a higher default clustering threshold (0.7), a single obvious
talker collapses to 1 while two genuinely distinct voices still resolve to 2.

When --num-clusters is given (>0) it pins the count and BOTH heuristics are
skipped, so --min-speakers/--max-speakers force an exact count as before.

sherpa-onnx does not expose a per-segment posterior, so no confidence is
emitted here; the Go caller assigns a sensible constant. On any failure this
prints {"skipped": true, "reason": "..."} and exits 0 so the Go caller
surfaces a clean error instead of a stack trace.

Verified against sherpa_onnx 1.13.2 (config field names from __init__ docstrings).
"""
import argparse
import array
import json
import os
import sys
import wave

SAMPLE_RATE = 16000
VAD_WINDOW = 512


def load_wav(path):
    """Read a mono PCM-16 WAV, returning (samples_float_list, sample_rate)."""
    with wave.open(path, "rb") as wf:
        sample_rate = wf.getframerate()
        n_channels = wf.getnchannels()
        sampwidth = wf.getsampwidth()
        raw = wf.readframes(wf.getnframes())
    if sampwidth != 2:
        raise ValueError(f"expected 16-bit PCM WAV, got sampwidth={sampwidth}")
    samples_int = array.array("h", raw)
    if n_channels > 1:
        # Downmix to mono by taking the first channel (Go side already extracts
        # mono, but stay defensive).
        samples_int = samples_int[0::n_channels]
    samples = [s / 32768.0 for s in samples_int]
    return samples, sample_rate


def vad_speech_spans(samples, sample_rate, vad_model, threshold,
                     min_silence, min_speech):
    """Run Silero VAD and return merged (start_sec, end_sec) speech spans.

    Mirrors vad_silero.py so diarization gates on the exact same speech model
    becky-vad/becky-cut use. Returns [] if no speech is found.
    """
    import sherpa_onnx

    cfg = sherpa_onnx.VadModelConfig()
    cfg.silero_vad.model = vad_model
    cfg.silero_vad.threshold = threshold
    cfg.silero_vad.min_silence_duration = min_silence
    cfg.silero_vad.min_speech_duration = min_speech
    cfg.sample_rate = SAMPLE_RATE

    duration = len(samples) / float(sample_rate) if sample_rate else 0.0
    vad = sherpa_onnx.VoiceActivityDetector(
        cfg, buffer_size_in_seconds=max(1.0, duration + 5.0)
    )
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
        vad.accept_waveform(samples[i:i + VAD_WINDOW])
        i += VAD_WINDOW
        drain()
    vad.flush()
    drain()

    # Clamp, sort, merge overlapping/adjacent spans.
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


def gate_to_speech(samples, sample_rate, spans):
    """Concatenate the speech spans into one waveform.

    Returns (gated_samples, mapping) where mapping is a list of
    (gated_start_sec, gated_end_sec, orig_start_sec) used to translate gated
    timestamps back onto the original timeline.
    """
    gated = []
    mapping = []
    cursor = 0.0
    for start, end in spans:
        a = int(round(start * sample_rate))
        b = int(round(end * sample_rate))
        chunk = samples[a:b]
        if not chunk:
            continue
        seg_dur = len(chunk) / float(sample_rate)
        mapping.append((cursor, cursor + seg_dur, start))
        gated.extend(chunk)
        cursor += seg_dur
    return gated, mapping


def map_time_back(t, mapping):
    """Translate a gated-timeline second back to the original timeline."""
    if not mapping:
        return t
    for gs, ge, orig_start in mapping:
        if gs - 1e-6 <= t <= ge + 1e-6:
            return orig_start + (t - gs)
    # Past the last span (rounding tail): extend from the final span.
    gs, ge, orig_start = mapping[-1]
    return orig_start + (t - gs)


def build_diarization(args):
    """Construct an OfflineSpeakerDiarization from the resolved model paths."""
    import sherpa_onnx

    for f in (args.seg_model, args.embedding_model):
        if not os.path.exists(f):
            raise FileNotFoundError(f"model not found: {f}")

    provider = "cuda" if args.device == "cuda" else "cpu"

    segmentation = sherpa_onnx.OfflineSpeakerSegmentationModelConfig(
        pyannote=sherpa_onnx.OfflineSpeakerSegmentationPyannoteModelConfig(
            model=args.seg_model
        ),
        num_threads=args.num_threads,
        debug=False,
        provider=provider,
    )
    embedding = sherpa_onnx.SpeakerEmbeddingExtractorConfig(
        model=args.embedding_model,
        num_threads=args.num_threads,
        debug=False,
        provider=provider,
    )
    # num_clusters=-1 lets the cosine threshold pick the speaker count; a fixed
    # num_clusters>0 pins it (used for --min/--max-speakers on the Go side).
    clustering = sherpa_onnx.FastClusteringConfig(
        num_clusters=args.num_clusters,
        threshold=args.threshold,
    )
    config = sherpa_onnx.OfflineSpeakerDiarizationConfig(
        segmentation=segmentation,
        embedding=embedding,
        clustering=clustering,
        min_duration_on=args.min_duration_on,
        min_duration_off=args.min_duration_off,
    )
    if not config.validate():
        raise ValueError("invalid OfflineSpeakerDiarizationConfig")
    return sherpa_onnx.OfflineSpeakerDiarization(config)


def progress_callback(verbose):
    if not verbose:
        return None

    def cb(num_processed, num_total):
        if num_total > 0:
            pct = 100.0 * num_processed / num_total
            print(f"diarization progress: {pct:.0f}%", file=sys.stderr, flush=True)
        return 0

    return cb


def merge_outlier_speakers(segments, min_dur, min_frac, verbose):
    """Reassign tiny outlier speakers into the nearest-in-time real speaker.

    `segments` is a list of (start, end, speaker_int). A speaker survives if its
    total speech time is >= min_dur AND >= min_frac of all speech. Outlier
    segments are reassigned to the temporally-nearest surviving speaker so the
    timeline stays coherent. Returns a new (start, end, speaker_int) list.

    This only runs in auto mode; it is the fix for the stubborn singleton cluster
    that a high clustering threshold cannot merge.
    """
    if not segments:
        return segments
    totals = {}
    grand_total = 0.0
    for start, end, spk in segments:
        d = end - start
        totals[spk] = totals.get(spk, 0.0) + d
        grand_total += d
    if len(totals) <= 1 or grand_total <= 0.0:
        return segments

    keep = {
        spk for spk, d in totals.items()
        if d >= min_dur and d >= min_frac * grand_total
    }
    if not keep:
        # Degenerate: everything is below threshold; keep the largest.
        keep = {max(totals, key=totals.get)}
    if keep == set(totals):
        return segments

    # Midpoints of surviving speakers' segments, for nearest-in-time matching.
    survivor_marks = []  # (midpoint_sec, speaker_int)
    for start, end, spk in segments:
        if spk in keep:
            survivor_marks.append(((start + end) / 2.0, spk))

    def nearest_survivor(start, end):
        mid = (start + end) / 2.0
        best_spk = None
        best_gap = None
        for m, spk in survivor_marks:
            gap = abs(m - mid)
            if best_gap is None or gap < best_gap:
                best_gap = gap
                best_spk = spk
        return best_spk

    out = []
    reassigned = 0
    for start, end, spk in segments:
        if spk not in keep:
            spk = nearest_survivor(start, end)
            reassigned += 1
        out.append((start, end, spk))
    if verbose and reassigned:
        dropped = sorted(set(totals) - keep)
        print(
            f"merged {len(dropped)} outlier speaker(s) "
            f"({reassigned} segment(s) reassigned)",
            file=sys.stderr, flush=True,
        )
    return out


def relabel_contiguous(segments):
    """Renumber speaker ints to a dense 0..N-1 ordered by first appearance."""
    mapping = {}
    out = []
    for start, end, spk in segments:
        if spk not in mapping:
            mapping[spk] = len(mapping)
        out.append((start, end, mapping[spk]))
    return out, len(mapping)


def run_diarization(sd, samples, verbose):
    """Run sherpa diarization, returning a list of (start, end, speaker_int)."""
    result = sd.process(samples, callback=progress_callback(verbose))
    segs = result.sort_by_start_time()
    return [(seg.start, seg.end, int(seg.speaker)) for seg in segs]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--seg-model", required=True)
    ap.add_argument("--embedding-model", required=True)
    ap.add_argument("--num-threads", type=int, default=4)
    ap.add_argument("--device", default="cpu")  # cpu | cuda
    ap.add_argument("--num-clusters", type=int, default=-1)  # -1 = auto
    ap.add_argument("--threshold", type=float, default=0.7)
    ap.add_argument("--min-duration-on", type=float, default=0.3)
    ap.add_argument("--min-duration-off", type=float, default=0.5)
    # Auto-mode-only robustness knobs (ignored when --num-clusters > 0).
    ap.add_argument("--vad-gate", dest="vad_gate", action="store_true", default=True,
                    help="gate diarization to Silero VAD speech regions (default)")
    ap.add_argument("--no-vad-gate", dest="vad_gate", action="store_false",
                    help="disable VAD speech-gating; diarize the whole file")
    ap.add_argument("--vad-model", default="", help="silero_vad.onnx (required for gating)")
    ap.add_argument("--vad-threshold", type=float, default=0.5)
    ap.add_argument("--vad-min-silence", type=float, default=0.1)
    ap.add_argument("--vad-min-speech", type=float, default=0.1)
    ap.add_argument("--min-speaker-duration", type=float, default=1.5,
                    help="auto mode: merge speakers with less total speech (s)")
    ap.add_argument("--min-speaker-frac", type=float, default=0.10,
                    help="auto mode: merge speakers below this fraction of speech")
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    sd = build_diarization(args)
    samples, file_sr = load_wav(args.audio)
    duration = len(samples) / float(file_sr) if file_sr else 0.0

    expected_sr = sd.sample_rate
    if file_sr != expected_sr:
        raise ValueError(
            f"sample rate mismatch: wav={file_sr} model expects {expected_sr}"
        )

    auto_mode = args.num_clusters <= 0
    use_gate = (
        auto_mode and args.vad_gate
        and args.vad_model and os.path.exists(args.vad_model)
    )

    mapping = None
    if use_gate:
        spans = vad_speech_spans(
            samples, file_sr, args.vad_model, args.vad_threshold,
            args.vad_min_silence, args.vad_min_speech,
        )
        if spans:
            gated, mapping = gate_to_speech(samples, file_sr, spans)
            if args.verbose:
                gated_dur = len(gated) / float(file_sr)
                print(
                    f"vad-gated to {len(spans)} speech span(s), "
                    f"{gated_dur:.1f}s of {duration:.1f}s",
                    file=sys.stderr, flush=True,
                )
            triples = run_diarization(sd, gated, args.verbose)
            triples = [
                (map_time_back(s, mapping), map_time_back(e, mapping), spk)
                for s, e, spk in triples
            ]
            triples.sort(key=lambda t: t[0])
        else:
            # VAD found no speech; fall back to full-file diarization.
            mapping = None
            triples = run_diarization(sd, samples, args.verbose)
    else:
        triples = run_diarization(sd, samples, args.verbose)

    if auto_mode:
        triples = merge_outlier_speakers(
            triples, args.min_speaker_duration, args.min_speaker_frac,
            args.verbose,
        )

    triples, num_speakers = relabel_contiguous(triples)

    out_segments = [
        {
            "start": round(start, 3),
            "end": round(end, 3),
            "speaker": f"SPEAKER_{spk:02d}",
        }
        for start, end, spk in triples
    ]

    print(json.dumps({
        "duration": round(duration, 3),
        "sample_rate": file_sr,
        "num_speakers": int(num_speakers),
        "segments": out_segments,
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
