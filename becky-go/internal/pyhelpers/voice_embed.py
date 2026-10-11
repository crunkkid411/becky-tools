#!/usr/bin/env python3
"""becky-identify helper: CAM++ speaker embedding (192-dim) via sherpa-onnx.

Thin binding over sherpa-onnx's SpeakerEmbeddingExtractor (the proven recipe from
kevs-obsidian-ingestion-engine/scripts/enroll_voice.py). Reads one or more 16 kHz
mono PCM WAVs and emits a single line of JSON to stdout:

  {"embeddings": [{"path": "...", "vector": [<192 floats>]}], "dim": 192}

Cosine similarity is intentionally NOT done here — the Go caller owns matching so
the math stays in one place and stays deterministic. Python is glue only; the
heavy compute runs in sherpa-onnx's C++/ONNX core.

On any failure this prints {"skipped": true, "reason": "..."} and exits 0 so the
Go caller surfaces a clean error instead of a stack trace.

Verified against sherpa_onnx 1.13.2 (CAM++ 3dspeaker_speech_campplus_sv_en_voxceleb_16k.onnx, dim 192).
"""
import argparse
import array
import json
import os
import sys
import wave


def load_wav(path):
    """Read a mono PCM-16 WAV, returning (samples_float, sample_rate)."""
    with wave.open(path, "rb") as wf:
        sample_rate = wf.getframerate()
        n_channels = wf.getnchannels()
        sampwidth = wf.getsampwidth()
        raw = wf.readframes(wf.getnframes())
    if sampwidth != 2:
        raise ValueError(f"expected 16-bit PCM WAV, got sampwidth={sampwidth}")
    samples_int = array.array("h", raw)
    if n_channels > 1:
        # Defensive downmix (Go side already extracts mono).
        samples_int = samples_int[0::n_channels]
    samples = [s / 32768.0 for s in samples_int]
    return samples, sample_rate


def make_extractor(model_path, num_threads, provider):
    import sherpa_onnx

    config = sherpa_onnx.SpeakerEmbeddingExtractorConfig(
        model=model_path,
        num_threads=num_threads,
        debug=False,
        provider=provider,
    )
    return sherpa_onnx.SpeakerEmbeddingExtractor(config)


VAD_WINDOW = 512


def gate_speech(samples, sample_rate, vad_model, threshold):
    """Return only the speech samples (Silero VAD), or all samples if none/short.

    Embeddings must be computed over speech ONLY. Comparing a clean-speech speaker
    embedding against a raw enrollment clip that still contains intro music / SFX
    tanks the cosine (the non-speech dominates the embedding). Gating BOTH sides the
    same way removes that asymmetry. Mirrors the VAD becky-diarize/becky-vad use.
    """
    import sherpa_onnx

    cfg = sherpa_onnx.VadModelConfig()
    cfg.silero_vad.model = vad_model
    cfg.silero_vad.threshold = threshold
    cfg.silero_vad.min_silence_duration = 0.1
    cfg.silero_vad.min_speech_duration = 0.1
    cfg.sample_rate = sample_rate

    duration = len(samples) / float(sample_rate) if sample_rate else 0.0
    vad = sherpa_onnx.VoiceActivityDetector(
        cfg, buffer_size_in_seconds=max(1.0, duration + 5.0)
    )
    speech = []

    def drain():
        while not vad.empty():
            speech.extend(vad.front.samples)
            vad.pop()

    i = 0
    while i < len(samples):
        vad.accept_waveform(samples[i:i + VAD_WINDOW])
        i += VAD_WINDOW
        drain()
    vad.flush()
    drain()

    # Fall back to the full clip if VAD found little/no speech, so short or quiet
    # enrollment clips still produce an embedding rather than failing.
    if len(speech) < int(0.5 * sample_rate):
        return samples
    return speech


class Redimnet:
    """ReDimNet2+ (lab260/redimnet2-plus, Apache-2.0), the model becky-identify NAMES voices with.

    model_dir holds pytorch_model_fsdp.bin, model_config.json (the public b6-vox2 release config the
    "+" checkpoint was tuned from; the model card's own loading snippet has the wrong shape) and
    code/ (the authors' repo). Needs torch + torchaudio (anaconda base, not the sherpa venv).
    The model was trained on <=60 s, so long audio is cut into 30 s windows and averaged.
    """

    WINDOW = 30 * 16000
    MIN_TAIL = 3 * 16000

    def __init__(self, model_dir, device):
        import types
        import torch
        code = os.path.join(model_dir, "code")
        sys.path.insert(0, code)
        # Load only the model files: asv/models/__init__.py imports the training stack (omegaconf, hydra).
        for name, sub in (("asv", "asv"), ("asv.models", os.path.join("asv", "models"))):
            mod = types.ModuleType(name)
            mod.__path__ = [os.path.join(code, sub)]
            sys.modules.setdefault(name, mod)
        from asv.models.redimnet2_vendor.redimnet2 import ReDimNet2Wrap
        with open(os.path.join(model_dir, "model_config.json"), encoding="utf-8") as f:
            self.model = ReDimNet2Wrap(**json.load(f))
        sd = torch.load(os.path.join(model_dir, "pytorch_model_fsdp.bin"), map_location="cpu", weights_only=True)  # nosec: weights_only=True loads tensors, never code
        prefix = "_orig_mod.encoder.model."
        self.model.load_state_dict({k[len(prefix):]: v for k, v in sd.items() if k.startswith(prefix)}, strict=True)
        self.device = "cuda" if device == "cuda" and torch.cuda.is_available() else "cpu"
        self.model.eval().to(self.device)
        self.torch = torch

    def embed(self, samples, sample_rate):
        if sample_rate != 16000:
            raise ValueError(f"ReDimNet2+ needs 16 kHz audio, got {sample_rate}")
        torch = self.torch
        wave = torch.tensor(samples, dtype=torch.float32)
        cuts = [wave[i:i + self.WINDOW] for i in range(0, len(wave), self.WINDOW)]
        if len(cuts) > 1 and len(cuts[-1]) < self.MIN_TAIL:
            cuts = cuts[:-1]
        vecs = []
        with torch.no_grad():
            for c in cuts:
                x = c[None].to(self.device)
                v = self.model(self.model.spec(x))[0]
                vecs.append(v / v.norm())
        mean = torch.stack(vecs).mean(0)
        return [float(t) for t in (mean / mean.norm()).cpu()]


def extract_one(extractor, path, vad_model="", vad_threshold=0.5):
    """Extract a single embedding from a WAV file (speech-gated)."""
    samples, sample_rate = load_wav(path)
    if not samples:
        raise RuntimeError(f"empty audio: {path}")
    if vad_model and os.path.exists(vad_model):
        samples = gate_speech(samples, sample_rate, vad_model, vad_threshold)
    if isinstance(extractor, Redimnet):
        return extractor.embed(samples, sample_rate)
    stream = extractor.create_stream()
    stream.accept_waveform(sample_rate, samples)
    stream.input_finished()
    if not extractor.is_ready(stream):
        raise RuntimeError(f"extractor not ready (audio too short?): {path}")
    embedding = extractor.compute(stream)
    return [float(x) for x in embedding]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("wavs", nargs="+", help="one or more 16 kHz mono PCM WAV paths")
    ap.add_argument("--model", required=True, help="CAM++ embedding ONNX model path, or the ReDimNet2+ dir with --backend redimnet")
    ap.add_argument("--backend", default="campp", choices=["campp", "redimnet"])
    ap.add_argument("--num-threads", type=int, default=2)
    ap.add_argument("--device", default="cpu")  # cpu | cuda
    ap.add_argument("--vad-model", default="", help="silero_vad.onnx; gate to speech before embedding")
    ap.add_argument("--vad-threshold", type=float, default=0.5)
    args = ap.parse_args()

    if not os.path.exists(args.model):
        raise FileNotFoundError(f"model not found: {args.model}")

    provider = "cuda" if args.device == "cuda" else "cpu"
    if args.backend == "redimnet":
        extractor = Redimnet(args.model, provider)
    else:
        extractor = make_extractor(args.model, args.num_threads, provider)

    out = []
    dim = 0
    for path in args.wavs:
        if not os.path.exists(path):
            raise FileNotFoundError(f"wav not found: {path}")
        try:
            vector = extract_one(extractor, path, args.vad_model, args.vad_threshold)
        except RuntimeError as e:  # too short / no speech: an empty vector, the other wavs still count
            print(f"voice_embed: {e}", file=sys.stderr)
            vector = []
        dim = max(dim, len(vector))
        out.append({"path": path, "vector": vector})

    print(json.dumps({"embeddings": out, "dim": dim}))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
