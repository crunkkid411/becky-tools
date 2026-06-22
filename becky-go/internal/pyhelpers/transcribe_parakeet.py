#!/usr/bin/env python3
"""becky-transcribe helper: Parakeet-TDT-0.6B-v3 (ONNX) via sherpa-onnx.

Thin binding over sherpa-onnx's C++/ONNX core. Reads a 16 kHz mono WAV and
emits one line of JSON to stdout:

  {"model","version","language","text",
   "words":[{"word","start","end","confidence"}],
   "device","fell_back"[,"fallback_reason"]}

Long files (multi-hour livestreams) are decoded in fixed TIME WINDOWS so that
RAM and GPU VRAM stay bounded regardless of duration. This is the DEFAULT
(--chunk-seconds 30); each window is ONE forward pass, so the window length (not
the file length) is what must fit in memory and under the model's positional
limit -- 15-min windows OOM'd and broke attention. A clip shorter than one window
is processed as a single pass. The recognizer is built ONCE per provider, not per
window.

Device selection (--device):
  * auto (default) -> try CUDA first, and if a window fails for ANY reason
    (out-of-memory is the common one), free the GPU, rebuild the recognizer on
    CPU ONCE, and finish the REMAINING windows on CPU. Windows already decoded on
    the GPU are kept, so a multi-hour file does not restart from zero. The GPU is
    used whenever it works (fast); CPU is the safety net (slow but never crashes).
  * cuda -> GPU only (no CPU fallback).
  * cpu  -> CPU only.
The "device" field reports which provider produced the FINAL window, and
"fell_back" is true when CUDA was tried first and we dropped to CPU.

On any failure it emits {"skipped": true, "reason": "..."} and exits 0 so the
Go caller can surface a clean error instead of a stack trace.

Ported from kevs-obsidian-ingestion-engine/scripts/asr_parakeet.py (proven).
"""
import argparse
import gc
import json
import os
import sys
import wave


def open_wav(path):
    """Open a 16 kHz mono 16-bit PCM WAV and return (wave_reader, sample_rate).

    The caller MUST close the reader. We do NOT read all frames here: windowed
    decoding seeks and reads one window at a time so memory stays bounded.
    """
    wf = wave.open(path, "rb")
    if wf.getsampwidth() != 2:
        # The Go caller always writes 16-bit PCM; refuse anything else rather
        # than misparse the bytes into a garbage (but plausible) transcript.
        sw = wf.getsampwidth() * 8
        wf.close()
        raise ValueError(f"expected 16-bit PCM WAV, got {sw}-bit")
    return wf, wf.getframerate()


def read_window(wf, start_frame, num_frames):
    """Read [start_frame, start_frame+num_frames) as float samples in [-1, 1).

    Reads ONLY this window's frames (via setpos), never the whole file, so VRAM
    and RAM scale with the window size, not the clip length.
    """
    import array

    total = wf.getnframes()
    if start_frame >= total or num_frames <= 0:
        return []
    if start_frame + num_frames > total:
        num_frames = total - start_frame
    wf.setpos(start_frame)
    raw = wf.readframes(num_frames)
    samples_int = array.array("h", raw)
    return [s / 32768.0 for s in samples_int]


def build_recognizer(provider, encoder, decoder, joiner, tokens, num_threads):
    """Construct the sherpa-onnx recognizer on the given provider (cpu|cuda)."""
    import sherpa_onnx

    return sherpa_onnx.OfflineRecognizer.from_transducer(
        encoder=encoder,
        decoder=decoder,
        joiner=joiner,
        tokens=tokens,
        num_threads=num_threads,
        decoding_method="greedy_search",
        model_type="nemo_transducer",  # required for NeMo Parakeet ONNX export
        provider=provider,
    )


def transcribe_once(recognizer, sample_rate, samples):
    """Run one full decode pass and return the sherpa-onnx result object."""
    stream = recognizer.create_stream()
    stream.accept_waveform(sample_rate, samples)
    recognizer.decode_stream(stream)
    return stream.result


def release_gpu():
    """Best-effort: drop GPU memory before a CPU retry (no-op if torch absent)."""
    gc.collect()
    try:
        import torch

        if torch.cuda.is_available():
            torch.cuda.empty_cache()
    except Exception:  # noqa: BLE001 - torch is optional; never let cleanup crash
        pass


def looks_like_oom(exc):
    """Heuristic: does this exception look like a GPU out-of-memory failure?"""
    m = f"{type(exc).__name__}: {exc}".lower()
    # Only memory-specific phrases — cuBLAS/cuDNN errors are often config/version
    # problems, not OOM, and mislabelling them as OOM hides the real cause. The
    # CPU fallback still happens on ANY error regardless of this label.
    needles = (
        "out of memory",
        "oom",
        "cudaerrormemoryallocation",
        "cuda_error_out_of_memory",
        "failed to allocate",
    )
    return any(n in m for n in needles)


def providers_for(device):
    """Map the --device choice to an ordered list of providers to try."""
    d = (device or "auto").lower()
    if d == "cpu":
        return ["cpu"]
    if d == "cuda":
        return ["cuda"]
    return ["cuda", "cpu"]  # auto: GPU first, CPU as the fallback


def build_words(result):
    """Merge sherpa-onnx BPE tokens+timestamps into (text, words[])."""
    text = result.text.strip()
    words = []
    toks = getattr(result, "tokens", None) or []
    times = getattr(result, "timestamps", None) or []
    cur_word, cur_start, cur_end = "", None, None
    for i, tok in enumerate(toks):
        t = float(times[i]) if i < len(times) else 0.0
        is_new = tok.startswith(" ") or tok.startswith("▁")
        clean = tok.lstrip(" ▁")
        if is_new:
            if cur_word.strip():
                words.append({
                    "word": cur_word.strip(),
                    "start": round(cur_start, 3),
                    "end": round(cur_end, 3),
                    "confidence": None,
                })
            cur_word, cur_start, cur_end = clean, t, t
        else:
            if cur_start is None:
                cur_start = t
            cur_word += clean
            cur_end = t
    if cur_word.strip():
        words.append({
            "word": cur_word.strip(),
            "start": round(cur_start or 0.0, 3),
            "end": round(cur_end or 0.0, 3),
            "confidence": None,
        })
    if not words and text:
        words = [{"word": text, "start": 0.0, "end": 0.0, "confidence": None}]
    return text, words


def window_words(result, offset, win_lo, win_hi, is_last):
    """Build a window's words, offset to absolute time, owned by exactly one window.

    `result` came from decoding window samples that START at `offset` seconds in
    the source, so every word time is shifted by `offset`. A word is kept only if
    its (offset-adjusted) START falls in [win_lo, win_hi) — the overlap tail past
    win_hi belongs to the NEXT window, which decoded those samples fully too, so
    every boundary word is fully decoded by at least one window and assigned to
    exactly one (no seam duplicates). The LAST window keeps everything to the end
    (win_hi is effectively +inf for it).
    """
    _text, raw = build_words(result)
    out = []
    for w in raw:
        start = round(w["start"] + offset, 3)
        end = round(w["end"] + offset, 3)
        if start < win_lo:
            # Word started before this window owns — left to the previous window.
            continue
        if not is_last and start >= win_hi:
            # Overlap tail — the next window owns this word.
            continue
        out.append({
            "word": w["word"],
            "start": start,
            "end": end,
            "confidence": w.get("confidence"),
        })
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--model-dir", required=True)
    ap.add_argument("--num-threads", type=int, default=4)
    ap.add_argument("--device", default="auto")  # auto | cuda | cpu
    ap.add_argument("--lang", default="en")
    # Windowed decoding keeps RAM/VRAM bounded on long files. Each window is ONE
    # forward pass, so the window length (not the file length) drives memory and
    # the model's positional-attention limit; default 30s windows with a 2 s
    # overlap so boundary words are fully decoded. (900s OOM'd / broke attention.)
    ap.add_argument("--chunk-seconds", type=float, default=30.0)
    ap.add_argument("--chunk-overlap", type=float, default=2.0)
    args = ap.parse_args()

    md = args.model_dir
    encoder = os.path.join(md, "encoder.int8.onnx")
    decoder = os.path.join(md, "decoder.int8.onnx")
    joiner = os.path.join(md, "joiner.int8.onnx")
    tokens = os.path.join(md, "tokens.txt")
    for f in (encoder, decoder, joiner, tokens):
        if not os.path.exists(f):
            print(json.dumps({"skipped": True, "reason": f"Model file not found: {f}"}))
            sys.exit(0)

    wf, sample_rate = open_wav(args.audio)
    try:
        total_frames = wf.getnframes()
        order = providers_for(args.device)
        if not order:
            print(json.dumps({"skipped": True, "reason": "no provider for device"}))
            sys.exit(0)

        # Window geometry in frames. chunk_seconds<=0 or a file shorter than one
        # window -> a single window covering everything (today's behaviour).
        step = args.chunk_seconds
        overlap = max(0.0, args.chunk_overlap)
        if step <= 0:
            step_frames = total_frames
            win_frames = total_frames
        else:
            step_frames = max(1, int(round(step * sample_rate)))
            win_frames = step_frames + int(round(overlap * sample_rate))
        if step_frames >= total_frames:
            num_windows = 1
            step_frames = total_frames
            win_frames = total_frames
        else:
            num_windows = (total_frames + step_frames - 1) // step_frames

        # Build the recognizer ONCE on the first provider; rebuild only on a
        # fallback (cuda -> cpu), not per window.
        prov_idx = 0
        cur_prov = order[prov_idx]
        recognizer = build_recognizer(
            cur_prov, encoder, decoder, joiner, tokens, args.num_threads
        )
        fell_back = False
        fallback_err = None

        all_words = []
        for i in range(num_windows):
            start_frame = i * step_frames
            is_last = i == num_windows - 1
            n_frames = total_frames - start_frame if is_last else win_frames
            samples = read_window(wf, start_frame, n_frames)
            if not samples:
                continue
            win_lo = round(start_frame / sample_rate, 3)
            win_hi = round((start_frame + step_frames) / sample_rate, 3)
            offset = win_lo

            try:
                result = transcribe_once(recognizer, sample_rate, samples)
            except Exception as e:  # noqa: BLE001 - per-window GPU failure -> fall back
                can_fallback = (
                    cur_prov == "cuda"
                    and prov_idx + 1 < len(order)
                    and order[prov_idx + 1] == "cpu"
                )
                why = "out of memory" if looks_like_oom(e) else f"{type(e).__name__}"
                if not can_fallback:
                    # device==cuda (no cpu) or already on cpu: nothing left to try.
                    raise
                sys.stderr.write(
                    f"[transcribe] {cur_prov} window {i + 1}/{num_windows} failed "
                    f"({why}); rebuilding on cpu and continuing...\n"
                )
                fallback_err = e
                recognizer = None  # drop the GPU session before rebuilding
                release_gpu()
                prov_idx += 1
                cur_prov = order[prov_idx]
                fell_back = True
                recognizer = build_recognizer(
                    cur_prov, encoder, decoder, joiner, tokens, args.num_threads
                )
                # Retry THIS window on cpu; all remaining windows run on cpu too.
                result = transcribe_once(recognizer, sample_rate, samples)

            all_words.extend(window_words(result, offset, win_lo, win_hi, is_last))

        text = " ".join(w["word"] for w in all_words).strip()
        out = {
            "model": "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8",
            "version": "v3",
            "language": args.lang,
            "text": text,
            "words": all_words,
            "device": cur_prov,
            "fell_back": fell_back,
        }
        if fell_back and fallback_err is not None:
            out["fallback_reason"] = f"{type(fallback_err).__name__}: {fallback_err}"[:300]
        print(json.dumps(out))
    finally:
        wf.close()


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
