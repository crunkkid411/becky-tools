#!/usr/bin/env python3
"""becky-transcribe helper: Parakeet-TDT-0.6B-v3 (ONNX) via sherpa-onnx.

Thin binding over sherpa-onnx's C++/ONNX core. Reads a 16 kHz mono WAV and
emits one line of JSON to stdout:

  {"model","version","language","text",
   "words":[{"word","start","end","confidence"}],
   "device","fell_back"[,"fallback_reason"]}

Device selection (--device):
  * auto (default) -> try CUDA first, and if the GPU run fails for ANY reason
    (out-of-memory is the common one), free the GPU and transparently re-run the
    WHOLE clip on CPU so a transcript is still produced. The GPU is used whenever
    it works (fast); CPU is the safety net (slow but never crashes).
  * cuda -> GPU only (no CPU fallback).
  * cpu  -> CPU only.
The "device" field reports which provider actually produced the result, and
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


def load_audio(path):
    """Read a 16 kHz mono PCM WAV into (sample_rate, float samples in [-1, 1))."""
    import array
    import wave

    with wave.open(path, "rb") as wf:
        if wf.getsampwidth() != 2:
            # The Go caller always writes 16-bit PCM; refuse anything else rather
            # than misparse the bytes into a garbage (but plausible) transcript.
            raise ValueError(f"expected 16-bit PCM WAV, got {wf.getsampwidth() * 8}-bit")
        sample_rate = wf.getframerate()
        raw = wf.readframes(wf.getnframes())
    samples_int = array.array("h", raw)
    samples = [s / 32768.0 for s in samples_int]
    return sample_rate, samples


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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--model-dir", required=True)
    ap.add_argument("--num-threads", type=int, default=4)
    ap.add_argument("--device", default="auto")  # auto | cuda | cpu
    ap.add_argument("--lang", default="en")
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

    sample_rate, samples = load_audio(args.audio)

    order = providers_for(args.device)
    last_err = None
    for i, prov in enumerate(order):
        recognizer = None
        try:
            recognizer = build_recognizer(
                prov, encoder, decoder, joiner, tokens, args.num_threads
            )
            result = transcribe_once(recognizer, sample_rate, samples)
        except Exception as e:  # noqa: BLE001 - any GPU failure should fall back
            last_err = e
            nxt = order[i + 1] if i + 1 < len(order) else None
            why = "out of memory" if looks_like_oom(e) else f"{type(e).__name__}"
            if nxt:
                sys.stderr.write(
                    f"[transcribe] {prov} run failed ({why}); falling back to {nxt}...\n"
                )
            else:
                sys.stderr.write(f"[transcribe] {prov} run failed ({why}); no fallback left.\n")
            recognizer = None  # drop the GPU session before retrying
            release_gpu()
            continue

        text, words = build_words(result)
        out = {
            "model": "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8",
            "version": "v3",
            "language": args.lang,
            "text": text,
            "words": words,
            "device": prov,
            "fell_back": i > 0,
        }
        if i > 0 and last_err is not None:
            out["fallback_reason"] = f"{type(last_err).__name__}: {last_err}"[:300]
        print(json.dumps(out))
        return

    reason = f"{type(last_err).__name__}: {last_err}" if last_err else "no provider succeeded"
    print(json.dumps({"skipped": True, "reason": reason}))
    sys.exit(0)


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
