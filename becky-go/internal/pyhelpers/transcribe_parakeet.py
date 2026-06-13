#!/usr/bin/env python3
"""becky-transcribe helper: Parakeet-TDT-0.6B-v3 (ONNX) via sherpa-onnx.

Thin binding over sherpa-onnx's C++/ONNX core. Reads a 16 kHz mono WAV and
emits one line of JSON to stdout:

  {"model","version","language","text",
   "words":[{"word","start","end","confidence"}]}

On any failure it emits {"skipped": true, "reason": "..."} and exits 0 so the
Go caller can surface a clean error instead of a stack trace.

Ported from kevs-obsidian-ingestion-engine/scripts/asr_parakeet.py (proven).
"""
import argparse
import json
import os
import sys


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--model-dir", required=True)
    ap.add_argument("--num-threads", type=int, default=4)
    ap.add_argument("--device", default="cpu")  # cpu | cuda
    ap.add_argument("--lang", default="en")
    args = ap.parse_args()

    import sherpa_onnx

    md = args.model_dir
    encoder = os.path.join(md, "encoder.int8.onnx")
    decoder = os.path.join(md, "decoder.int8.onnx")
    joiner = os.path.join(md, "joiner.int8.onnx")
    tokens = os.path.join(md, "tokens.txt")
    for f in (encoder, decoder, joiner, tokens):
        if not os.path.exists(f):
            print(json.dumps({"skipped": True, "reason": f"Model file not found: {f}"}))
            sys.exit(0)

    provider = "cuda" if args.device == "cuda" else "cpu"
    recognizer = sherpa_onnx.OfflineRecognizer.from_transducer(
        encoder=encoder,
        decoder=decoder,
        joiner=joiner,
        tokens=tokens,
        num_threads=args.num_threads,
        decoding_method="greedy_search",
        model_type="nemo_transducer",  # required for NeMo Parakeet ONNX export
        provider=provider,
    )

    import array
    import wave

    with wave.open(args.audio, "rb") as wf:
        sample_rate = wf.getframerate()
        raw = wf.readframes(wf.getnframes())
        samples_int = array.array("h", raw)
        samples = [s / 32768.0 for s in samples_int]

    stream = recognizer.create_stream()
    stream.accept_waveform(sample_rate, samples)
    recognizer.decode_stream(stream)
    result = stream.result
    text = result.text.strip()

    # sherpa-onnx returns BPE tokens; a leading space (or U+2581) marks a new
    # word, anything else is a sub-word continuation. Merge into words.
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

    print(json.dumps({
        "model": "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8",
        "version": "v3",
        "language": args.lang,
        "text": text,
        "words": words,
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
