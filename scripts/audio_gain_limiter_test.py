"""Diagnostic, not a maintained tool - kept for reference (SKILL.md's ROUGH CUT
section cites this by name). Tests Jordan's suggestion (2026-08-24): does
pushing the roughcut ANALYSIS-chain gain ~12dB further, with a REAL limiter
(not the current tanh softclip, and not the compressor already measured to
crush ~20dB of dynamic range) give a cleaner speech/room boundary on his real
Rode-mic footage? Real clip, real word timings - not a synthetic tone.

Result (measured on HJOC7106 + SNOW_20260823122254, both real clips): no -
pushing gain into a limiter compresses the loud end (speech) more than the
already-quiet room tone (which stays under the limiter's threshold and just
scales linearly), so it NARROWS the speech/room gap instead of widening it.
Current chain was equal-or-better on every real word-boundary onset tested.
Full writeup: SKILL.md, "Why not auto-editor / becky-cut / loudnorm here".

Usage: python audio_gain_limiter_test.py [STEM] [WINDOW_END_SEC]
  e.g. python audio_gain_limiter_test.py SNOW_20260823122254 30
Needs ffmpeg on PATH and a real becky-roughcut _roughcut/ output dir (reads
<STEM>.dossier.json for the real calibrated gain, <STEM>.words.json for real
word timings) next to the source footage.
"""
import json
import math
import struct
import subprocess
import sys
import wave

FFMPEG = "ffmpeg"
STEM = sys.argv[1] if len(sys.argv) > 1 else "HJOC7106"
WINDOW_END = float(sys.argv[2]) if len(sys.argv) > 2 else 25.0
ROOT = "X:/Videos/2026/08_august/23_hj-fbi-recap"
SRC = ROOT + "/" + STEM + (".MP4" if STEM == "HJOC7106" else ".mp4")
WORDS = ROOT + "/_roughcut/" + STEM + ".words.json"
CURRENT_GAIN = json.load(open(ROOT + "/_roughcut/" + STEM + ".dossier.json", encoding="utf-8"))["gain_db"]


def run(*args):
    subprocess.run(list(args), check=True, capture_output=True)


def read_wav_mono16(path):
    with wave.open(path, "rb") as w:
        assert w.getsampwidth() == 2 and w.getnchannels() == 1
        sr = w.getframerate()
        raw = w.readframes(w.getnframes())
        n = len(raw) // 2
        samples = struct.unpack("<%dh" % n, raw)
        return sr, [s / 32768.0 for s in samples]


def dbfs(x):
    return 20 * math.log10(x) if x > 1e-9 else -120.0


def rms_env_db(samples, sr, win_sec=0.4):
    win = max(1, int(win_sec * sr))
    out = []
    for i in range(0, len(samples), win):
        chunk = samples[i:i + win]
        if not chunk:
            continue
        rms = math.sqrt(sum(s * s for s in chunk) / len(chunk))
        out.append(dbfs(rms))
    return out


def percentile(vals, q):
    s = sorted(vals)
    return s[int(q * (len(s) - 1))]


def db_at(samples, sr, t0, t1):
    i0, i1 = int(t0 * sr), int(t1 * sr)
    chunk = samples[max(0, i0):max(0, i1)]
    if not chunk:
        return -120.0
    rms = math.sqrt(sum(s * s for s in chunk) / len(chunk))
    return dbfs(rms)


def peak_fraction(samples, thresh=0.98):
    return sum(1 for s in samples if abs(s) >= thresh) / len(samples)


raw_wav, cur_wav, prop_wav = "raw.wav", "cur.wav", "prop.wav"

run(FFMPEG, "-y", "-hide_banner", "-nostdin", "-ss", "0", "-to", str(WINDOW_END),
    "-i", SRC, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", raw_wav)

# current chain: exactly what detect.go's normalize() runs
run(FFMPEG, "-y", "-hide_banner", "-nostdin", "-i", raw_wav, "-af",
    f"highpass=f=80,volume={CURRENT_GAIN:.1f}dB,asoftclip=type=tanh",
    "-c:a", "pcm_s16le", cur_wav)

# proposed chain: Jordan's ask - 12dB more gain, a real true-peak limiter
# (limit=0.891 ~= -1dBFS ceiling) instead of the softclip
PROPOSED_GAIN = CURRENT_GAIN + 12.0
run(FFMPEG, "-y", "-hide_banner", "-nostdin", "-i", raw_wav, "-af",
    f"highpass=f=80,volume={PROPOSED_GAIN:.1f}dB,alimiter=limit=0.891:attack=1:release=50",
    "-c:a", "pcm_s16le", prop_wav)

sr, raw_s = read_wav_mono16(raw_wav)
_, cur_s = read_wav_mono16(cur_wav)
_, prop_s = read_wav_mono16(prop_wav)

words = json.load(open(WORDS, encoding="utf-8"))["words"]
words = [w for w in words if w["start"] < WINDOW_END]

print(f"=== whole-window RMS envelope, 0.4s windows (current gain {CURRENT_GAIN:.1f}dB, proposed {PROPOSED_GAIN:.1f}dB) ===")
for name, s in [("raw (unboosted)", raw_s), ("current (gain+tanh softclip)", cur_s), ("proposed (+12dB, real limiter)", prop_s)]:
    env = rms_env_db(s, sr)
    p90, p10 = percentile(env, 0.90), percentile(env, 0.10)
    print(f"{name:32s} p90(speech)={p90:7.2f}dB  p10(room)={p10:7.2f}dB  separation={p90 - p10:6.2f}dB  peak>=0.98 frac={peak_fraction(s):.4f}")

print()
print("=== word-boundary jump: 100ms before word start (should be room) -> 100ms of the word (should be speech) ===")
print("    (only word starts that follow a real >=0.3s gap - a true onset, not mid-sentence coarticulation)")
prev_end, tested = 0.0, 0
for w in words:
    gap = w["start"] - prev_end
    prev_end = max(prev_end, w["end"])
    if gap < 0.3 or tested >= 8:
        continue
    tested += 1
    t = w["start"]
    row = [f"  word={w['word']!r:12s} @ {t:6.2f}s (gap {gap:.2f}s)"]
    for name, s in [("current", cur_s), ("proposed", prop_s)]:
        before = db_at(s, sr, t - 0.1, t)
        after = db_at(s, sr, t, t + 0.1)
        row.append(f"{name}: before={before:7.2f}dB after={after:7.2f}dB jump={after - before:6.2f}dB")
    print("  ".join(row))
