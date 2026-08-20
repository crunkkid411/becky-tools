"""Does Marlin-2B answer the RULE 4 questions? Run it and see.

    scripts/marlin_probe.py <video.mp4>

Needs the dedicated venv - see research/model-marlin-2b-TESTED.md for why a venv
and for the measured timings. Run it with:

    X:/AI-2/becky-tools/.venv-marlin/Scripts/python.exe scripts/marlin_probe.py <video>

On CPU this takes about a minute of wall clock per second of video. On the GPU it
is seconds. The GPU was not visible to the session that first ran this.


Two things becky cannot do today:
  caption() -> Scene + Events with second-precise <start-end> boundaries
  find(event) -> the (start, end) span of a described moment

The second one IS Jordan's rule 4 request in one call: "the very first frame in
which it starts to move".
"""
import os, sys, time

# Match the training-time preprocessing (the card documents these). Set before
# transformers is imported.
os.environ.setdefault("FORCE_QWENVL_VIDEO_READER", "torchcodec")
os.environ.setdefault("VIDEO_MAX_PIXELS", "200704")
os.environ.setdefault("FPS", "2.0")
os.environ.setdefault("FPS_MAX_FRAMES", "64")   # short clip; keeps CPU inference sane
os.environ.setdefault("FPS_MIN_FRAMES", "4")

import torch
from transformers import AutoModelForCausalLM

VIDEO = sys.argv[1]
cuda = torch.cuda.is_available()
dev = "cuda" if cuda else "cpu"
dtype = torch.bfloat16 if cuda else torch.float32
print(f"device={dev} dtype={dtype} torch={torch.__version__}", flush=True)

t0 = time.time()
marlin = AutoModelForCausalLM.from_pretrained(
    "NemoStation/Marlin-2B",
    trust_remote_code=True,
    dtype=dtype,
    device_map={"": dev},
)
print(f"loaded in {time.time()-t0:.1f}s", flush=True)

t0 = time.time()
res = marlin.caption(VIDEO, max_new_tokens=512)
print(f"\n=== caption ({time.time()-t0:.1f}s) ===", flush=True)
print("SCENE:", res.get("scene"))
for ev in res.get("events", []):
    print(f"  <{ev['start']:.1f} - {ev['end']:.1f}> {ev['description']}")

for q in ("the snake starts to move",
          "a person reacts with a shocked facial expression"):
    t0 = time.time()
    f = marlin.find(VIDEO, event=q)
    print(f"\n=== find({q!r}) ({time.time()-t0:.1f}s) ===", flush=True)
    print("  raw:", f.get("raw"), "| span:", f.get("span"), "| format_ok:", f.get("format_ok"))
