# Frame-level sound event detection over a whole 16 kHz WAV with the
# PretrainedSED AudioSet-Strong models (MIT). Writes 40 ms frame probabilities.
# usage: breath-sed-probe.py <BEATs|ATST-F|frame_mn10> <in.wav> <out.npz>
# Setup: git clone https://github.com/fschmid56/PretrainedSED next to this file;
# runs from anaconda base + einops (see research/breath-vs-movement-sound-labels.md).
import os
import sys
import time

import numpy as np
import soundfile as sf
import torch
import torch.nn.functional as F

REPO = os.path.join(os.path.dirname(os.path.abspath(__file__)), "PretrainedSED")
sys.path.insert(0, REPO)

name, wav, out = sys.argv[1], os.path.abspath(sys.argv[2]), os.path.abspath(sys.argv[3])
os.chdir(REPO)  # checkpoints download to PretrainedSED/resources

from data_util import audioset_classes  # noqa: E402
from models.prediction_wrapper import PredictionsWrapper  # noqa: E402

dev = torch.device("cuda" if torch.cuda.is_available() else "cpu")
if name == "BEATs":
    from models.beats.BEATs_wrapper import BEATsWrapper
    model = PredictionsWrapper(BEATsWrapper(), checkpoint="BEATs_strong_1")
elif name == "ATST-F":
    from models.atstframe.ATSTF_wrapper import ATSTWrapper
    model = PredictionsWrapper(ATSTWrapper(), checkpoint="ATST-F_strong_1")
elif name.startswith("frame_mn"):
    from models.frame_mn.Frame_MN_wrapper import FrameMNWrapper
    from models.frame_mn.utils import NAME_TO_WIDTH
    fm = FrameMNWrapper(NAME_TO_WIDTH(name))
    dim = fm.state_dict()["frame_mn.features.16.1.bias"].shape[0]
    model = PredictionsWrapper(fm, checkpoint=f"{name}_strong_1", embed_dim=dim)
else:
    raise SystemExit("unknown model " + name)
model.eval().to(dev)

x, sr = sf.read(wav, dtype="float32")
assert sr == 16000, sr
if x.ndim > 1:
    x = x.mean(axis=1)
w = torch.from_numpy(x)[None].to(dev)
seg = 10 * 16000  # the models are trained on 10 s pieces, 250 frames each
t0 = time.time()
parts = []
for i in range(0, w.shape[1], seg):
    ch = w[:, i:i + seg]
    n = ch.shape[1]
    if n < seg:
        ch = F.pad(ch, (0, seg - n))
    with torch.no_grad():
        y, _ = model(model.mel_forward(ch))  # (1, classes, 250)
    y = torch.sigmoid(y)[0].T.float().cpu().numpy()
    parts.append(y[: int(round(250 * n / seg))])
P = np.concatenate(parts)
np.savez_compressed(out, probs=P.astype(np.float16),
                    classes=np.array(audioset_classes.as_strong_train_classes), hop=0.04)
print(f"{name}: {P.shape[0]} frames x {P.shape[1]} classes in {time.time() - t0:.1f}s on {dev}")
if dev.type == "cuda":
    print(f"peak GPU memory {torch.cuda.max_memory_allocated() / 2**30:.2f} GB")
