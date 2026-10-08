import os
d = os.path.abspath(".")
rows = [["fx", "460", "90", "VEGAS Pixelate", "CENSOR"],
        ["zoom", "700", "120", "1.6", "0.5", "0.35", "15"],
        ["fx", "900", "60", "VEGAS Picture In Picture", "IN"],
        ["duck", "1100", "30", "-60"],
        ["audio", "1100", os.path.join(d, "bleep-1k.wav"), "Becky Bleeps"]]
open("job.txt", "w", encoding="utf-8").write("".join("\t".join(r) + "\n" for r in rows))
