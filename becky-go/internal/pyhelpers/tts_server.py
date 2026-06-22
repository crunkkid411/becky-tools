#!/usr/bin/env python3
"""becky-tts WARM server: load NeuTTS Air ONCE, synth many times fast.

The per-call CLI (tts_neutts.py) reloads torch + the model on every invocation
(~35s) which is unusable for a GUI "Speak" button. This server loads the model a
single time and then answers POST /speak in the time it takes to synthesize — so
the canvas can speak instantly after a one-time warm-up.

Endpoints (tiny stdlib http.server, no extra deps):
  GET  /health           -> {"status":"ok","model":...} once the model is loaded
  POST /speak            -> body audio/wav.  JSON in: {"text","voice"?,"seed"?}

The GGUF backbone runs on the GPU when a CUDA llama-cpp is installed
(BECKY_TTS_BACKBONE_DEVICE=gpu, the default here) so generation is fast; NeuCodec
decodes on CPU (small cost). Degrade-never-crash: a synth failure returns HTTP 500
with a plain reason; the caller keeps the visual UI and never gets a Microsoft voice.

Run:  python tts_server.py --model <gguf-or-dir> [--port 11436]
Env:  BECKY_TTS_NEUCODEC, BECKY_TTS_SAMPLES, BECKY_TTS_BACKBONE_DEVICE (gpu|cpu),
      BECKY_TTS_CODEC_DEVICE (cpu|cuda), PHONEMIZER_ESPEAK_LIBRARY/_PATH.
"""
import argparse
import io
import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

# Reuse the proven resolution + espeak wiring from the per-call helper so the two
# stay identical (one source of truth for reference voice + neucodec + espeak).
sys.path.insert(0, str(Path(__file__).resolve().parent))
import tts_neutts as helper  # noqa: E402

DEFAULT_PORT = 11436

_engine = None
_engine_lock = threading.Lock()
_ref_cache = {}


def log(msg: str) -> None:
    print(f"tts_server: {msg}", file=sys.stderr, flush=True)


def load_engine(model: str):
    """Load NeuTTS Air once. Backbone on GPU by default (fast); codec on CPU."""
    helper._setup_espeak()
    import numpy as np  # noqa: F401  (ensures the stack is importable before serving)

    NeuTTS = None
    try:
        from neutts import NeuTTS as _N

        NeuTTS = _N
    except Exception:
        from neuttsair.neutts import NeuTTSAir as _N

        NeuTTS = _N

    backbone = str(Path(model))
    codec = helper._resolve_neucodec()
    lang = os.environ.get("BECKY_TTS_LANG", "en-us")
    bdev = os.environ.get("BECKY_TTS_BACKBONE_DEVICE", "gpu")
    cdev = os.environ.get("BECKY_TTS_CODEC_DEVICE", "cpu")
    log(f"loading backbone={backbone} ({bdev}) codec={codec} ({cdev}) lang={lang}")
    eng = NeuTTS(
        backbone_repo=backbone,
        backbone_device=bdev,
        codec_repo=codec,
        codec_device=cdev,
        language=lang,
    )
    log("model loaded — ready")
    return eng


def synth(text: str, voice: str, seed: int) -> bytes:
    """Synthesize text -> WAV bytes (PCM16 mono 24 kHz) using the warm engine."""
    import numpy as np
    import soundfile as sf

    helper_dir = Path(__file__).resolve().parent
    ref_audio, ref_text = helper._resolve_reference(voice, helper_dir)
    with _engine_lock:  # the LM is single-threaded; serialize requests
        key = ref_audio
        codes = _ref_cache.get(key)
        if codes is None:
            codes = _engine.encode_reference(ref_audio)
            _ref_cache[key] = codes
        wav = _engine.infer(text, codes, ref_text)
    buf = io.BytesIO()
    sf.write(buf, np.asarray(wav).squeeze(), 24000, format="WAV", subtype="PCM_16")
    return buf.getvalue()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):  # quiet; we log our own lines
        pass

    def do_GET(self):
        if self.path.rstrip("/") in ("/health", ""):
            ok = _engine is not None
            self._json(200 if ok else 503, {"status": "ok" if ok else "loading"})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path.rstrip("/") != "/speak":
            self._json(404, {"error": "not found"})
            return
        try:
            n = int(self.headers.get("Content-Length", "0"))
            req = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            self._json(400, {"error": f"bad request: {e}"})
            return
        text = str(req.get("text", "")).strip()
        if not text:
            self._json(400, {"error": "empty text"})
            return
        voice = str(req.get("voice", "default")).strip() or "default"
        try:
            seed = int(req.get("seed", 42))
        except Exception:
            seed = 42
        try:
            wav = synth(text, voice, seed)
        except Exception as e:
            self._json(500, {"error": f"synthesis failed: {e}"})
            return
        self.send_response(200)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("Content-Length", str(len(wav)))
        self.end_headers()
        self.wfile.write(wav)

    def _json(self, code: int, obj: dict) -> None:
        body = json.dumps(obj).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main() -> None:
    ap = argparse.ArgumentParser(description="becky-tts warm NeuTTS Air server")
    ap.add_argument("--model", required=True, help="NeuTTS Air GGUF file or safetensors dir")
    ap.add_argument("--port", type=int, default=int(os.environ.get("BECKY_TTS_PORT", DEFAULT_PORT)))
    args = ap.parse_args()
    if not Path(args.model).exists():
        log(f"model not found: {args.model}")
        sys.exit(2)

    global _engine
    try:
        _engine = load_engine(args.model)
    except Exception as e:
        log(f"could not load NeuTTS: {e}")
        sys.exit(1)

    srv = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    log(f"listening on http://127.0.0.1:{args.port} (POST /speak, GET /health)")
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
