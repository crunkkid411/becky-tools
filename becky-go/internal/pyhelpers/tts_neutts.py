#!/usr/bin/env python3
"""becky-tts helper: NeuTTS Air (Neuphonic) text -> WAV.

Thin binding over Neuphonic's `neutts` package. Honours the EXACT argv that
`internal/tts.NeuTTSArgs` emits so the Go `becky-tts` can shell out to it via
BECKY_TTS_BIN:

    --model <gguf>   the NeuTTS Air GGUF LM backbone (llama.cpp-class)
    --text  <text>   the words to speak
    --out   <wav>    where to write the PCM WAV
    --voice <name|sample.wav>   "default" (stock reference) or a reference clip
    --seed  <n>      determinism seed (default 42)
    --rate  <hz>     OPTIONAL output sample rate (default = NeuTTS native 24000)

NeuTTS Air is a voice-CLONING model: it needs a reference audio clip AND that
clip's transcript. "default" uses the bundled `samples/dave.wav` (+ dave.txt).
A `--voice X.wav` uses X.wav as the reference and reads its transcript from a
sibling `X.txt` (falls back to the default transcript if absent).

Degrade-never-crash: any failure prints a plain reason to stderr and exits
non-zero, so the Go side raises a typed DegradeError and prints the text instead
of a stack trace. It NEVER substitutes a Microsoft voice.

Backbone (GGUF LM) runs via llama-cpp-python (CPU is fine for the 0.75B q4).
The NeuCodec decoder is resolved from BECKY_TTS_NEUCODEC (default the local
`models/tts/neucodec` dir) so nothing hits the network at runtime.
"""
import argparse
import os
import random
import sys
from pathlib import Path
from typing import NoReturn


def _die(reason: str, code: int = 1) -> NoReturn:
    """Print a plain-language reason to stderr and exit non-zero (degrade path)."""
    print(f"tts_neutts: {reason}", file=sys.stderr)
    sys.exit(code)


def _setup_espeak() -> None:
    """Point phonemizer at the system espeak-ng (must happen before import).

    phonemizer reads PHONEMIZER_ESPEAK_LIBRARY / _PATH at import time. We resolve
    the Windows install (or honour an explicit override) so the user never has to
    put espeak-ng on PATH.
    """
    lib = os.environ.get("PHONEMIZER_ESPEAK_LIBRARY", "")
    exe = os.environ.get("PHONEMIZER_ESPEAK_PATH", "")
    candidates_lib = [
        lib,
        r"C:\Program Files\eSpeak NG\libespeak-ng.dll",
        r"C:\Program Files (x86)\eSpeak NG\libespeak-ng.dll",
    ]
    candidates_exe = [
        exe,
        r"C:\Program Files\eSpeak NG\espeak-ng.exe",
        r"C:\Program Files (x86)\eSpeak NG\espeak-ng.exe",
    ]
    for c in candidates_lib:
        if c and Path(c).exists():
            os.environ["PHONEMIZER_ESPEAK_LIBRARY"] = c
            break
    for c in candidates_exe:
        if c and Path(c).exists():
            os.environ["PHONEMIZER_ESPEAK_PATH"] = c
            break


def _resolve_reference(voice: str, helper_dir: Path) -> tuple[str, str]:
    """Return (ref_audio_path, ref_text) for the requested voice.

    "default"/"" -> the bundled dave sample. A path to a .wav -> that clip plus
    its sibling .txt transcript. Anything else is treated as the default preset.
    """
    if os.environ.get("BECKY_TTS_SAMPLES"):
        samples = Path(os.environ["BECKY_TTS_SAMPLES"])
    else:
        samples = _samples_dir(helper_dir)
    default_wav = samples / "dave.wav"
    default_txt = samples / "dave.txt"

    v = (voice or "").strip()
    if v and v.lower() != "default":
        p = Path(v)
        if p.exists() and p.suffix.lower() == ".wav":
            txt = p.with_suffix(".txt")
            ref_text = txt.read_text(encoding="utf-8").strip() if txt.exists() else _default_text(default_txt)
            return str(p), ref_text
        # Not a usable clip -> fall through to the default preset.
    if not default_wav.exists():
        _die(
            f"reference voice not found: {default_wav} "
            "(download samples/dave.wav + dave.txt into the models/tts/samples dir)"
        )
    return str(default_wav), _default_text(default_txt)


def _default_text(default_txt: Path) -> str:
    if default_txt.exists():
        return default_txt.read_text(encoding="utf-8").strip()
    # Last-resort transcript for dave.wav (matches the published sample).
    return (
        "So I'm live on radio. And I say, well, my dear friend James here clearly, "
        "and the whole room just froze. Turns out I'd completely misspoken and "
        "mentioned our other friend."
    )


def _samples_dir(helper_dir: Path) -> Path:
    """Best-effort locate the becky models/tts/samples dir."""
    for c in [
        Path(r"X:\AI-2\becky-tools\models\tts\samples"),
        helper_dir.parent.parent.parent / "models" / "tts" / "samples",
    ]:
        if c.exists():
            return c
    return Path(r"X:\AI-2\becky-tools\models\tts\samples")


def _resolve_neucodec() -> str:
    """Return the NeuCodec repo id NeuTTS accepts.

    NeuTTS._load_codec matches on EXACT repo-id strings ("neuphonic/neucodec",
    "neuphonic/distill-neucodec", "neuphonic/neucodec-onnx-decoder"[-int8]) and
    rejects a local directory path. So we pass the supported id; from_pretrained
    resolves it from the HF cache (becky downloads it once, then it is offline).
    An override is honoured only if it is one of those recognised ids.
    """
    supported = {
        "neuphonic/neucodec",
        "neuphonic/distill-neucodec",
        "neuphonic/neucodec-onnx-decoder",
        "neuphonic/neucodec-onnx-decoder-int8",
    }
    env = os.environ.get("BECKY_TTS_NEUCODEC", "").strip()
    if env in supported:
        return env
    return "neuphonic/neucodec"


def main() -> None:
    ap = argparse.ArgumentParser(description="NeuTTS Air text->WAV helper for becky-tts")
    ap.add_argument("--model", required=True, help="NeuTTS Air GGUF LM backbone path")
    ap.add_argument("--text", required=True, help="text to speak")
    ap.add_argument("--out", required=True, help="output WAV path")
    ap.add_argument("--voice", default="default", help='"default" or a reference .wav')
    ap.add_argument("--seed", type=int, default=42, help="determinism seed")
    ap.add_argument("--rate", type=int, default=0, help="output sample rate (0=native 24000)")
    args = ap.parse_args()

    text = (args.text or "").strip()
    if not text:
        _die("nothing to speak (empty text)")

    helper_dir = Path(__file__).resolve().parent

    # Determinism BEFORE importing torch.
    os.environ.setdefault("PYTHONHASHSEED", str(args.seed))
    _setup_espeak()

    try:
        import numpy as np
        import soundfile as sf
    except Exception as e:  # pragma: no cover - import guard
        _die(f"missing python deps (numpy/soundfile): {e}")

    random.seed(args.seed)
    try:
        np.random.seed(args.seed % (2**32 - 1))
    except Exception:
        pass
    try:
        import torch

        torch.manual_seed(args.seed)
    except Exception:
        pass

    # NeuTTS import (package is `neutts`; older builds expose `neuttsair`).
    NeuTTS = None
    try:
        from neutts import NeuTTS as _N

        NeuTTS = _N
    except Exception:
        try:
            from neuttsair.neutts import NeuTTSAir as _N

            NeuTTS = _N
        except Exception as e:
            _die(f"NeuTTS package not importable (pip install neutts): {e}")

    model = args.model
    if not Path(model).exists():
        _die(f"NeuTTS backbone not found: {model}")
    # NeuTTS._load_backbone picks the path by suffix: a ".gguf" FILE -> llama_cpp
    # (fast, but the prebuilt wheel needs an AVX2 build that matches the CPU);
    # anything else (a dir of safetensors, or a repo id) -> transformers/torch.
    # We pass --model straight through so becky can use either: the GGUF when a
    # matching llama-cpp wheel is installed, else the safetensors dir on torch.
    backbone = str(Path(model))
    codec = _resolve_neucodec()
    # NeuTTS can only infer the eSpeak language from a known repo id; we pass a
    # file path, so set it explicitly (NeuTTS Air is English).
    language = os.environ.get("BECKY_TTS_LANG", "en-us")

    ref_audio, ref_text = _resolve_reference(args.voice, helper_dir)

    try:
        tts = NeuTTS(
            backbone_repo=backbone,
            backbone_device=os.environ.get("BECKY_TTS_BACKBONE_DEVICE", "cpu"),
            codec_repo=codec,
            codec_device=os.environ.get("BECKY_TTS_CODEC_DEVICE", "cpu"),
            language=language,
        )
    except Exception as e:
        _die(f"could not load NeuTTS (backbone={backbone}, codec={codec}, lang={language}): {e}")

    try:
        ref_codes = tts.encode_reference(ref_audio)
        wav = tts.infer(text, ref_codes, ref_text)
    except Exception as e:
        _die(f"synthesis failed: {e}")

    rate = args.rate if args.rate and args.rate > 0 else 24000
    try:
        sf.write(args.out, np.asarray(wav).squeeze(), rate)
    except Exception as e:
        _die(f"could not write WAV {args.out}: {e}")

    if not Path(args.out).exists() or Path(args.out).stat().st_size < 64:
        _die(f"runtime wrote no usable WAV at {args.out}")
    print(f"tts_neutts: wrote {args.out} ({Path(args.out).stat().st_size} bytes @ {rate} Hz)", file=sys.stderr)


if __name__ == "__main__":
    main()
