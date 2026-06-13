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


def extract_one(extractor, path, vad_model="", vad_threshold=0.5):
    """Extract a single 192-float embedding from a WAV file (speech-gated)."""
    samples, sample_rate = load_wav(path)
    if not samples:
        raise RuntimeError(f"empty audio: {path}")
    if vad_model and os.path.exists(vad_model):
        samples = gate_speech(samples, sample_rate, vad_model, vad_threshold)
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
    ap.add_argument("--model", required=True, help="CAM++ embedding ONNX model path")
    ap.add_argument("--num-threads", type=int, default=2)
    ap.add_argument("--device", default="cpu")  # cpu | cuda
    ap.add_argument("--vad-model", default="", help="silero_vad.onnx; gate to speech before embedding")
    ap.add_argument("--vad-threshold", type=float, default=0.5)
    args = ap.parse_args()

    if not os.path.exists(args.model):
        raise FileNotFoundError(f"model not found: {args.model}")

    provider = "cuda" if args.device == "cuda" else "cpu"
    extractor = make_extractor(args.model, args.num_threads, provider)

    out = []
    dim = 0
    for path in args.wavs:
        if not os.path.exists(path):
            raise FileNotFoundError(f"wav not found: {path}")
        vector = extract_one(extractor, path, args.vad_model, args.vad_threshold)
        dim = len(vector)
        out.append({"path": path, "vector": vector})

    print(json.dumps({"embeddings": out, "dim": dim}))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
