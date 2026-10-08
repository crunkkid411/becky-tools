import os
d = os.path.abspath(".")
rows = [("440", "130", "censor.mp4"), ("690", "140", "zoom.mp4"), ("885", "90", "punchin.mp4"), ("1085", "60", "bleep.mp4")]
open("render.txt", "w", encoding="utf-8").write("".join(f"{a}\t{b}\t{os.path.join(d, c)}\n" for a, b, c in rows))
