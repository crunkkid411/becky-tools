#!/usr/bin/env python3
"""becky-transcribe GPU helper: Parakeet-TDT via onnx-asr on DirectML.

Runs the SAME Parakeet model as transcribe_parakeet.py, but through onnx-asr +
onnxruntime-directml, which accelerates the int8 model on the GPU through
DirectX 12 with NO CUDA / cuDNN setup -- the exact approach the Rust app "Handy"
uses. Measured ~4-5x faster than the sherpa-onnx CPU path on an RTX 3070 (about
57x realtime), so a 2-hour stream transcribes in ~2 minutes instead of ~15. If
DirectML can't load it falls back to CPU, so it never hard-fails.

It emits the SAME one-line JSON contract as transcribe_parakeet.py so the Go
caller (cmd/transcribe) is unchanged:

  {"model","version","language","text",
   "words":[{"word","start","end","confidence"}],
   "device","fell_back"[,"fallback_reason"]}

Word timings (root fix, 2026-10-05). The old builder used each word's LAST
TOKEN START as its end, so most words came out zero-length. Parakeet-TDT
predicts a DURATION for every token it emits; onnx-asr uses it to advance but
throws it away. We keep it (a tiny patch of onnx-asr's decode loop), so a token
spans [t, t+duration) and a word ends where its last token ends -- the same rule
NeMo uses for TDT word timestamps.

Windows (root fix, same day). Long files are decoded in ~30s windows (Parakeet's
safe context). The old scheme started each window cold at a fixed time and
threw away the context-rich 2s tail, so the first words of a window were
decoded with no lead-in and whole sentence openings went missing. Now each
window's boundary is moved to the QUIETEST moment near the 30s mark, the model
hears --chunk-overlap seconds of audio on BOTH sides, and the window keeps only
the words whose midpoint falls inside its own stretch. On any failure it prints
{"skipped":true,"reason":...} and exits 0 so the Go caller surfaces a clean
error instead of a stack trace. `--selftest` checks the pure logic (no model).

Setup (scripts/setup-asr-gpu.ps1): a venv with `onnx-asr`, `onnxruntime-directml`,
`soundfile`, `huggingface_hub`. The model is the int8 Parakeet (v3 by default),
the same family becky already uses.
"""
import argparse
import json
import math
import os
import sys


def _log(msg):
    sys.stderr.write(msg + "\n")


def read_audio(path):
    """Read a WAV as float32 mono + its sample rate (onnx-asr resamples to 16k
    internally if needed). The Go caller already writes 16 kHz mono."""
    import soundfile as sf

    audio, sr = sf.read(path, dtype="float32")
    if getattr(audio, "ndim", 1) > 1:
        audio = audio.mean(axis=1)
    return audio, sr


def providers_for(device):
    """Map --device to an onnxruntime provider list. 'auto' prefers DirectML
    (GPU) then CPU; 'dml' is GPU-only; 'cpu' is CPU-only."""
    d = (device or "auto").lower()
    if d == "cpu":
        return ["CPUExecutionProvider"], "cpu"
    if d in ("dml", "directml", "gpu"):
        return ["DmlExecutionProvider"], "dml"
    return ["DmlExecutionProvider", "CPUExecutionProvider"], "auto"


# Encoder frame length in seconds: onnx-asr's 10 ms feature step x Parakeet's
# 8x subsampling. Overwritten from the live model by the decode patch.
FRAME_SEC = 0.08

# One list of per-token durations (in encoder frames) per decoded waveform,
# filled by the patched decode loop below. The helper decodes one window at a
# time, so it clears this before each call and reads entry [0] after.
_DURATIONS = []


def patch_tdt_durations(asr_module):
    """Re-define onnx-asr's transducer decode loop so it also records each
    emitted token's TDT duration (the frames that token occupies). The loop is
    otherwise a line-for-line copy of onnx-asr 0.11.0's. Returns False (and
    leaves onnx-asr untouched) if its internals have changed."""
    cls = getattr(asr_module, "_AsrWithTransducerDecoding", None)
    log_softmax = getattr(asr_module, "log_softmax", None)
    if cls is None or log_softmax is None or not hasattr(cls, "_decoding"):
        return False
    import numpy as np

    def _decoding(self, encoder_out, encoder_out_lens, /, **kwargs):
        global FRAME_SEC
        try:
            FRAME_SEC = float(self.window_step) * int(self._subsampling_factor)
        except Exception:  # noqa: BLE001 - keep the default frame length
            pass
        need_logprobs = kwargs.get("need_logprobs")
        if self.use_low_precision:
            encoder_out_lens = np.minimum(encoder_out_lens, encoder_out.shape[1])
        for encodings, encodings_len in zip(encoder_out, encoder_out_lens, strict=True):
            prev_state = self._create_state()
            tokens, timestamps, logprobs, durations = [], [], [], []
            t = 0
            emitted_tokens = 0
            while t < encodings_len:
                logits, step, state = self._decode(tokens, prev_state, encodings[t])
                token = logits.argmax()
                if token != self._blank_idx:
                    prev_state = state
                    tokens.append(int(token))
                    timestamps.append(t)
                    durations.append(max(int(step), 0))
                    emitted_tokens += 1
                    if need_logprobs:
                        logprobs.append(log_softmax(logits)[token])
                if step > 0:
                    t += step
                    emitted_tokens = 0
                elif token == self._blank_idx or emitted_tokens == self._max_tokens_per_step:
                    t += 1
                    emitted_tokens = 0
            _DURATIONS.append(durations)
            yield tokens, timestamps, logprobs if need_logprobs else None

    cls._decoding = _decoding
    return True


def token_ends(times, durations, frame_sec):
    """Per-token END seconds: a token emitted at t with duration d spans
    [t, t+d) frames. A zero duration means the next token came out on the same
    frame, so the token is given one frame. Without durations (patch failed),
    every token gets one frame -- short, but never zero-length."""
    if durations is None or len(durations) != len(times):
        return [float(t) + frame_sec for t in times]
    return [float(t) + max(int(d), 1) * frame_sec for t, d in zip(times, durations)]


def merge_tokens_to_words(tokens, times, logprobs, ends=None):
    """Merge onnx-asr BPE tokens into words. A token with a leading space (or the
    NeMo '_' marker) starts a new word; the rest continue it. `times` are
    per-token START seconds and `ends` per-token END seconds (chunk-relative); a
    word runs from its first token's start to its LAST token's END.
    Confidence = exp(mean token logprob), a real 0..1 signal for downstream
    confidence checks. Returns chunk-relative words."""
    if ends is None:
        ends = token_ends(times, None, FRAME_SEC)
    words = []
    cur, start, end, lps = "", None, None, []

    def flush():
        if cur.strip():
            conf = None
            if lps:
                conf = round(min(1.0, math.exp(sum(lps) / len(lps))), 4)
            s = start or 0.0
            words.append({
                "word": cur.strip(),
                "start": round(s, 3),
                "end": round(max(end if end is not None else s, s), 3),
                "confidence": conf,
            })

    for i, tok in enumerate(tokens):
        t = float(times[i]) if i < len(times) else (end or 0.0)
        e = float(ends[i]) if i < len(ends) else t + FRAME_SEC
        lp = float(logprobs[i]) if (logprobs is not None and i < len(logprobs)) else None
        if tok.startswith(" ") or tok.startswith("▁"):
            flush()
            cur, start, end, lps = tok.lstrip(" ▁"), t, e, ([lp] if lp is not None else [])
        else:
            if start is None:
                start = t
            cur += tok.lstrip(" ▁")
            # Parakeet often emits a word's "." or "?" only when the NEXT speech
            # starts, seconds later; punctuation carries no sound, so it never
            # stretches the word (measured: "Sage?" ran 7 s without this).
            if end is None or any(c.isalnum() for c in tok):
                end = e
            if lp is not None:
                lps.append(lp)
    flush()
    return words


def plan_windows(audio, sr, chunk_sec, search_sec=3.0):
    """Split the file into ~chunk_sec stretches whose boundaries sit at the
    quietest 0.3 s near each nominal chunk mark (searched +/- search_sec), so a
    boundary lands in a pause instead of mid-word. Returns [(lo, hi)] sample
    ranges covering the whole file in order."""
    import numpy as np

    total = len(audio)
    step_n = int(round(chunk_sec * sr))
    if total == 0 or step_n <= 0 or step_n >= total:
        return [(0, total)]
    hop = max(1, sr // 100)  # 10 ms energy frames
    n = total // hop
    frames = np.asarray(audio[: n * hop], dtype=np.float64).reshape(n, hop)
    energy = (frames ** 2).mean(axis=1)
    smooth = np.convolve(energy, np.ones(30) / 30.0, mode="same")  # 0.3 s
    search = int(round(search_sec * sr / hop))
    bounds = [0]
    nominal = step_n
    while nominal < total - step_n // 2:
        c = nominal // hop
        lo_f = max(c - search, bounds[-1] // hop + step_n // (2 * hop))
        hi_f = min(c + search, n - 1)
        b = nominal if hi_f <= lo_f else (lo_f + int(np.argmin(smooth[lo_f:hi_f + 1]))) * hop
        bounds.append(b)
        nominal = b + step_n
    bounds.append(total)
    return list(zip(bounds[:-1], bounds[1:]))


def own_window_words(words, offset, own_lo, own_hi, is_last):
    """Shift chunk-relative words to absolute time and keep only those this
    window owns: MIDPOINT in [own_lo, own_hi) seconds. The LAST window keeps
    everything to the end. Each word is decoded with audio on both sides of the
    stretch, and the neighbouring window owns whatever falls outside it."""
    out = []
    for w in words:
        start = round(w["start"] + offset, 3)
        end = round(w["end"] + offset, 3)
        mid = (start + end) / 2.0
        if mid < own_lo:
            continue
        if not is_last and mid >= own_hi:
            continue
        out.append({
            "word": w["word"],
            "start": start,
            "end": end,
            "confidence": w.get("confidence"),
        })
    return out


def selftest():
    """Assert-based check of the pure logic (no model, no GPU)."""
    import numpy as np

    # 1. Word ends come from the LAST token's end, never its start.
    toks = [" hel", "lo", " world"]
    times = [0.0, 0.16, 0.48]
    ends = token_ends(times, [2, 3, 0], 0.08)
    w = merge_tokens_to_words(toks, times, None, ends)
    assert [x["word"] for x in w] == ["hello", "world"], w
    assert w[0]["start"] == 0.0 and abs(w[0]["end"] - 0.40) < 1e-9, w[0]
    assert abs(w[1]["end"] - 0.56) < 1e-9, w[1]  # zero duration -> one frame
    assert all(x["end"] > x["start"] for x in w)
    # A late "?" (emitted seconds after the word) must not stretch the word.
    w = merge_tokens_to_words([" Sa", "ge", "?"], [1.0, 1.08, 7.0],
                              None, token_ends([1.0, 1.08, 7.0], [1, 2, 1], 0.08))
    assert w[0]["word"] == "Sage?" and abs(w[0]["end"] - 1.24) < 1e-9, w
    # 2. Window boundaries snap to the quiet stretch near each 30 s mark.
    sr = 16000
    rng = np.random.default_rng(0)
    audio = (rng.standard_normal(sr * 95) * 0.3).astype(np.float32)
    audio[int(31.5 * sr):int(32.5 * sr)] = 0.0  # pause just after 30 s
    audio[int(63.0 * sr):int(64.0 * sr)] = 0.0  # pause near the next mark (~62 s)
    wins = plan_windows(audio, sr, 30.0)
    assert wins[0][0] == 0 and wins[-1][1] == len(audio), wins
    assert all(a[1] == b[0] for a, b in zip(wins, wins[1:])), wins
    assert len(wins) == 3, [(a / sr, b / sr) for a, b in wins]
    assert 31.5 * sr <= wins[0][1] <= 32.5 * sr, wins[0][1] / sr
    assert 63.0 * sr <= wins[1][1] <= 64.0 * sr, wins[1][1] / sr
    # 3. Ownership by midpoint: a word straddling the boundary goes to one window.
    words = [{"word": "a", "start": 9.0, "end": 9.4}, {"word": "b", "start": 9.8, "end": 10.4}]
    assert [x["word"] for x in own_window_words(words, 0.0, 0.0, 10.0, False)] == ["a"]
    assert [x["word"] for x in own_window_words(words, 0.0, 10.0, 20.0, False)] == ["b"]
    print("selftest ok")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--model-name", default="nemo-parakeet-tdt-0.6b-v3",
                    help="onnx-asr model id (downloaded/cached) when --model-dir is empty")
    ap.add_argument("--model-dir", default="",
                    help="local onnx-asr model dir (encoder/decoder_joint/nemo128); "
                         "overrides --model-name")
    ap.add_argument("--quantization", default="int8")
    ap.add_argument("--device", default="auto")  # auto | dml | cpu
    ap.add_argument("--lang", default="en")
    ap.add_argument("--chunk-seconds", type=float, default=30.0)
    ap.add_argument("--chunk-overlap", type=float, default=2.0,
                    help="seconds of extra audio the model hears on EACH side of a window")
    ap.add_argument("--spans", default="",
                    help="JSON file of [[start,end],...] seconds: re-listen to ONLY these "
                         "stretches (each in its own fresh window) and report the words heard "
                         "in each -- the 'second look' when only the second pass heard speech")
    if "--selftest" in sys.argv:
        selftest()
        return
    args = ap.parse_args()

    try:
        import onnxruntime as ort

        ort.set_default_logger_severity(3)  # silence DML "node not assigned" spam
    except Exception:  # noqa: BLE001 - logging tweak is best-effort
        pass

    try:
        import onnx_asr
        import onnx_asr.asr as onnx_asr_core
    except Exception as e:  # noqa: BLE001
        print(json.dumps({"skipped": True, "reason": f"onnx-asr not available: {e}"}))
        sys.exit(0)
    have_durations = patch_tdt_durations(onnx_asr_core)
    if not have_durations:
        _log("[transcribe-dml] onnx-asr internals changed; word ends fall back to one frame")

    audio, sr = read_audio(args.audio)
    providers, want = providers_for(args.device)

    # Load the model; on a DirectML failure, fall back to CPU once.
    fell_back = False
    fallback_reason = None
    load_kwargs = dict(quantization=args.quantization)
    name_or_path = (args.model_name, args.model_dir or None)
    try:
        model = onnx_asr.load_model(*name_or_path, providers=providers, **load_kwargs)
        device = "dml" if providers and providers[0].startswith("Dml") else "cpu"
    except Exception as e:  # noqa: BLE001 - DML init can fail; retry on CPU
        if any(p.startswith("Dml") for p in providers):
            fell_back = True
            fallback_reason = f"{type(e).__name__}: {e}"[:300]
            _log(f"[transcribe-dml] DirectML load failed ({type(e).__name__}); falling back to CPU")
            model = onnx_asr.load_model(*name_or_path, providers=["CPUExecutionProvider"], **load_kwargs)
            device = "cpu"
        else:
            print(json.dumps({"skipped": True, "reason": f"model load failed: {e}"}))
            sys.exit(0)

    ts_model = model.with_timestamps()

    def decode(s, e):
        """Decode audio[s:e] (samples) -> words in absolute seconds, or None."""
        seg = audio[s:e]
        if len(seg) == 0:
            return None
        _DURATIONS.clear()
        r = ts_model.recognize(seg, sample_rate=sr)
        times = list(r.timestamps)
        durs = _DURATIONS[0] if (have_durations and len(_DURATIONS) == 1) else None
        rel = merge_tokens_to_words(list(r.tokens), times,
                                    list(r.logprobs) if r.logprobs is not None else None,
                                    token_ends(times, durs, FRAME_SEC))
        off = s / sr
        return [dict(w, start=round(w["start"] + off, 3), end=round(w["end"] + off, 3)) for w in rel]

    if args.spans:
        with open(args.spans, encoding="utf-8") as f:
            spans = json.load(f)
        ctx = max(0.0, args.chunk_overlap) + 1.0
        out_spans = []
        for a, b in spans:
            s = max(0, int((a - ctx) * sr))
            e = min(len(audio), int((b + ctx) * sr))
            try:
                ws = decode(s, e) or []
            except Exception as ex:  # noqa: BLE001 - one bad span must not kill the rest
                _log(f"[transcribe-dml] span {a:.2f}-{b:.2f} failed: {type(ex).__name__}: {ex}")
                ws = []
            keep = [w for w in ws if a - 0.25 <= (w["start"] + w["end"]) / 2.0 < b + 0.25]
            out_spans.append({"start": a, "end": b, "words": keep})
        print(json.dumps({"model": f"onnx-asr-{args.model_name}", "device": device, "spans": out_spans}))
        return

    pad_n = int(round(max(0.0, args.chunk_overlap) * sr))
    total = len(audio)
    windows = plan_windows(audio, sr, max(0.1, args.chunk_seconds))
    num_windows = len(windows)

    all_words = []
    for i, (lo, hi) in enumerate(windows):
        is_last = i == num_windows - 1
        try:
            ws = decode(max(0, lo - pad_n), min(total, hi + pad_n))
        except Exception as ex:  # noqa: BLE001 - a bad window must not kill the file
            _log(f"[transcribe-dml] window {i + 1}/{num_windows} failed: {type(ex).__name__}: {ex}")
            continue
        if ws:
            all_words.extend(own_window_words(ws, 0.0, round(lo / sr, 3), round(hi / sr, 3), is_last))

    text = " ".join(w["word"] for w in all_words).strip()
    out = {
        "model": f"onnx-asr-{args.model_name}",
        "version": "v3" if "v3" in args.model_name else ("v2" if "v2" in args.model_name else ""),
        "language": args.lang,
        "text": text,
        "words": all_words,
        "device": device,
        "fell_back": fell_back,
    }
    if fell_back and fallback_reason:
        out["fallback_reason"] = fallback_reason
    print(json.dumps(out))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
