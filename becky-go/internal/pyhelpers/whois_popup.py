"""whois_popup.py - becky's ONE "who is this?" window (opened by internal/whois.Show).

Jordan, 2026-10-11: "it can pop up and ask me who someone is. We've tried this in the past
and it was too annoying to use, so make sure it's actually functional this time and not
counterintuitive." So this window:
  - shows every open question in ONE window (never stacks), the clearest frame with the
    face boxed, a close-up, where it is from, becky's guess and what becky already checked;
  - answers with ONE click on a full name (1-9 on the keyboard), "Play this moment" plays
    6 seconds around it; typing is only for someone becky does not know yet, and the list
    under the box shows matching people (name or alias) so nobody is added twice;
  - teaches becky: the answer copies a head-and-shoulders picture into that person's
    face-prints, so the same face is not asked about again;
  - "I don't know" closes the question for good; "Ask me later" (or the X) hides it for 2 hours.
High-contrast colours on black on purpose: they are Jordan's reading aid.

  pythonw whois_popup.py --dir <whois dir> [--ffplay ffplay.exe]
  python whois_popup.py --selftest
"""
import argparse
import datetime
import json
import os
import re
import subprocess
import sys

BG, FG = "#000000", "#FFFFFF"
PINK, GREEN, CYAN, AMBER, GREY = "#FF2E97", "#39FF14", "#00E5FF", "#FFB000", "#B0B0B0"
SNOOZE_HOURS = 2
ALIVE_EVERY_MS = 2000
MAX_HINTS = 4
HINT_ROOM = 190  # screen space kept free below the window for the typed-name list


def read_jsonl(path):
    out = []
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line:
                    try:
                        out.append(json.loads(line))
                    except ValueError:
                        pass
    except FileNotFoundError:
        pass
    return out


def pending(d):
    done = {a.get("id") for a in read_jsonl(os.path.join(d, "answers.jsonl"))}
    return [q for q in read_jsonl(os.path.join(d, "questions.jsonl")) if q.get("id") not in done]


def clock(t):
    m, s = divmod(int(t + 0.5), 60)
    h, m = divmod(m, 60)
    return f"{h}:{m:02d}:{s:02d}" if h else f"{m}:{s:02d}"


def core(name):
    return name.split("(")[0].strip()


def matches(people, text):
    """People whose name or alias contains what was typed (case-insensitive), best first."""
    t = text.strip().lower()
    if len(t) < 2:
        return []
    out = []
    for p in people:
        names = [p["name"]] + list(p.get("aliases") or [])
        if any(t in n.lower() for n in names):
            starts = any(n.lower().startswith(t) for n in names)
            out.append((0 if starts else 1, p["name"], p))
    return [p for _, _, p in sorted(out, key=lambda x: (x[0], x[1]))]


def can_add(hits, text):
    """A new person needs a full name when one word already matches known people: a last
    name like "Clancy" belongs to a whole family and must never become a person."""
    if any(core(p["name"]).lower() == text.lower() for p in hits):
        return False
    return len(text.split()) >= 2 or not hits


def safe_dir(name):
    return re.sub(r'[<>:"/\\|?*]', "", name).strip().rstrip(".")


def head_and_shoulders(img, box):
    x1, y1, x2, y2 = box
    w = max(x2 - x1, 1)
    cx = (x1 + x2) / 2
    left, right = max(0, cx - 1.1 * w), min(img.width, cx + 1.1 * w)
    top, bottom = max(0, y1 - 0.6 * w), min(img.height, y2 + 0.9 * w)
    return img.crop((int(left), int(top), int(right), int(bottom)))


def blocked(kb, video):
    """True when the video is on the knowledge base's do-not-enroll list (e.g. AI-generated)."""
    try:
        with open(os.path.join(kb, "DO-NOT-ENROLL.txt"), encoding="utf-8") as f:
            return os.path.basename(video).lower() in f.read().lower()
    except FileNotFoundError:
        return False


def enroll(q, name, folder):
    """Copy a head-and-shoulders picture of the face into kb/face-prints/<folder>."""
    from PIL import Image
    kb = q.get("kb") or ""
    if not kb or not q.get("frame") or not q.get("box"):
        return "", "no picture to save"
    if blocked(kb, q.get("video", "")):
        return "", "this video is on the do-not-enroll list, so the picture was not added"
    dest = os.path.join(kb, "face-prints", safe_dir(folder))
    os.makedirs(dest, exist_ok=True)
    stem = re.sub(r"[^A-Za-z0-9_-]+", "_", os.path.splitext(os.path.basename(q["video"]))[0])[:60]
    path = os.path.join(dest, f"whois_{stem}_{q.get('at', 0):.1f}s.jpg")
    with Image.open(q["frame"]) as img:
        head_and_shoulders(img.convert("RGB"), q["box"]).save(path, quality=95)
    return path, ""


def add_person(kb, name):
    """A new person: an entities/<name>.json record so every becky tool knows the name."""
    ent = os.path.join(kb, "entities")
    os.makedirs(ent, exist_ok=True)
    path = os.path.join(ent, safe_dir(name).lower() + ".json")
    if not os.path.exists(path):
        with open(path, "w", encoding="utf-8") as f:
            json.dump({"name": name, "aliases": [], "description":
                       "Named by Jordan in the becky who-is-this window, " + datetime.date.today().isoformat()}, f, indent=2)


def write_answer(d, q, answer, enrolled):
    rec = {"id": q["id"], "video": q.get("video", ""), "at": q.get("at", 0), "guess": q.get("guess", ""),
           "answer": answer, "enrolled": enrolled, "answered_at": datetime.datetime.now().isoformat(timespec="seconds")}
    with open(os.path.join(d, "answers.jsonl"), "a", encoding="utf-8") as f:
        f.write(json.dumps(rec, ensure_ascii=False) + "\n")


class Window:
    def __init__(self, d, ffplay):
        import tkinter as tk
        self.tk, self.d, self.ffplay = tk, d, ffplay
        self.queue = pending(d)
        self.i = 0
        self.root = tk.Tk()
        self.root.title("becky - who is this?")
        self.root.configure(bg=BG)
        self.root.protocol("WM_DELETE_WINDOW", self.later)
        self.root.bind("<Escape>", lambda e: self.later())
        for n in range(1, 10):
            self.root.bind(str(n), lambda e, n=n: self.key(n))
        self.body = tk.Frame(self.root, bg=BG, padx=24, pady=16)
        self.body.pack(fill="both", expand=True)
        self.alive()
        self.show()
        self.root.attributes("-topmost", True)  # appear in front once, then behave like any window
        self.root.after(2500, lambda: self.root.attributes("-topmost", False))
        self.root.lift()
        self.root.focus_force()

    # --- queue ---
    def alive(self):
        with open(os.path.join(self.d, "popup.alive"), "w") as f:
            f.write(str(os.getpid()))
        have = {q["id"] for q in self.queue}
        self.queue += [q for q in pending(self.d) if q["id"] not in have]  # new questions join this window
        if hasattr(self, "count"):
            self.count.config(text=f"question {self.i + 1} of {len(self.queue)}")
        self.root.after(ALIVE_EVERY_MS, self.alive)

    def close(self):
        try:
            os.remove(os.path.join(self.d, "popup.alive"))
        except OSError:
            pass
        self.root.destroy()

    def later(self):
        until = datetime.datetime.now().astimezone() + datetime.timedelta(hours=SNOOZE_HOURS)
        with open(os.path.join(self.d, "snooze_until"), "w") as f:
            f.write(until.isoformat(timespec="seconds"))
        self.close()

    # --- one question ---
    def label(self, parent, text, color=FG, size=16, bold=False, **kw):
        lab = self.tk.Label(parent, text=text, fg=color, bg=BG, font=("Segoe UI", size, "bold" if bold else "normal"),
                            justify="left", anchor="w", **kw)
        return lab

    def button(self, parent, text, color, cmd, fg="#000000", size=16):
        return self.tk.Button(parent, text=text, command=cmd, bg=color, fg=fg, activebackground=FG,
                              activeforeground="#000000", font=("Segoe UI", size, "bold"), relief="flat",
                              padx=16, pady=6 if size > 12 else 2, cursor="hand2", borderwidth=0)

    def show(self):
        tk = self.tk
        for w in self.body.winfo_children():
            w.destroy()
        if self.i >= len(self.queue):
            self.label(self.body, "All done - thank you!", GREEN, 24, True).pack(pady=40)
            self.root.after(1500, self.close)
            return
        q = self.queue[self.i]
        self.q, self.people_keys = q, []
        top = tk.Frame(self.body, bg=BG)
        top.pack(fill="x")
        title = "Who is talking here?" if q.get("speaker") else "Who is this?"
        self.label(top, title, PINK, 26, True).pack(side="left")
        self.count = self.label(top, f"question {self.i + 1} of {len(self.queue)}", FG, 16)
        self.count.pack(side="right")
        self.status = self.label(top, "", GREEN, 16, True)  # "Saved: ..." shows here, so the window never grows
        self.status.pack(side="left", padx=24)

        pics = tk.Frame(self.body, bg=BG, pady=10)
        pics.pack(fill="x")
        self.images = []  # tk forgets images that nothing holds on to
        try:
            from PIL import Image, ImageDraw, ImageTk
            with Image.open(q["frame"]) as img:
                img = img.convert("RGB")
                shown = []
                if q.get("box"):  # a face question; a who-is-talking question shows the whole frame
                    face = head_and_shoulders(img, q["box"])
                    face.thumbnail((240, 240))
                    shown.append(face)
                frame = img.copy()
                if q.get("box"):
                    ImageDraw.Draw(frame).rectangle(q["box"], outline=GREEN, width=max(4, frame.width // 200))
                frame.thumbnail((520, 240) if q.get("box") else (640, 300))
                shown.append(frame)
            for im in shown:
                ph = ImageTk.PhotoImage(im)
                self.images.append(ph)
                tk.Label(pics, image=ph, bg=BG).pack(side="left", padx=(0, 16))
        except Exception as e:  # a missing picture must not hide the question
            self.label(pics, f"(picture missing: {e})", GREY, 12).pack(side="left")

        where = tk.Frame(self.body, bg=BG)
        where.pack(fill="x")
        self.label(where, f"From {os.path.basename(q.get('video', ''))}, at {clock(q.get('at', 0))}", FG, 16).pack(side="left")
        self.button(where, "Play this moment", CYAN, self.play, size=13).pack(side="left", padx=16)
        if q.get("guess"):
            self.label(self.body, f"becky's best guess: {q['guess']} - but it could not be sure.", AMBER, 16, True).pack(fill="x", pady=(8, 0))
        if q.get("checked"):
            self.label(self.body, "Already checked: " + q["checked"], GREY, 12, wraplength=900).pack(fill="x")

        self.label(self.body, "Click who it is:", FG, 16, True).pack(fill="x", pady=(14, 4))
        row = tk.Frame(self.body, bg=BG)
        row.pack(fill="x")
        people = q.get("people") or []
        guess = [p for p in people if p["name"] == q.get("guess")]
        rest = [p for p in people if p.get("has_face") and p["name"] != q.get("guess")]
        for n, p in enumerate((guess + rest)[:9], 1):
            self.people_keys.append(p)
            self.button(row, f"{n}   {p['name']}", GREEN if p in guess else CYAN,
                        lambda p=p: self.answer(p["name"], p.get("dir") or p["name"])).pack(side="left", padx=(0, 10), pady=4)

        self.label(self.body, "Someone else? Type their name:", FG, 14).pack(fill="x", pady=(14, 2))
        self.entry = tk.Entry(self.body, font=("Segoe UI", 16), bg=FG, fg="#000000", insertbackground="#000000", width=40)
        self.entry.pack(anchor="w")
        self.entry.bind("<KeyRelease>", lambda e: self.suggest())
        self.entry.bind("<Return>", lambda e: self.typed())
        self.hints = tk.Frame(self.body, bg=BG)
        self.hints.pack(fill="x", pady=4)

        bottom = tk.Frame(self.body, bg=BG, pady=12)
        bottom.pack(fill="x")
        self.button(bottom, "I don't know", GREY, lambda: self.answer("unknown", ""), size=14).pack(side="left")
        self.button(bottom, f"Ask me later ({SNOOZE_HOURS} hours)", BG, self.later, fg=FG, size=14).pack(side="right")
        self.root.after(80, self.place)  # Windows ignores a position set before the window first appears

    def place(self):
        """Centre left-right; high enough that the typed-name list never reaches the taskbar."""
        self.root.update_idletasks()
        w, h = self.root.winfo_width(), self.root.winfo_height()
        sw, sh = self.root.winfo_screenwidth(), self.root.winfo_screenheight()
        self.root.geometry(f"+{max(0, (sw - w) // 2)}+{max(0, min((sh - h) // 3, sh - 80 - h - HINT_ROOM))}")

    def suggest(self):
        for w in self.hints.winfo_children():
            w.destroy()
        text = self.entry.get().strip()
        if not text:
            return
        hits = matches(self.q.get("people") or [], text)
        for p in hits[:MAX_HINTS]:  # one per line, so a long name never runs off the window
            self.button(self.hints, p["name"], AMBER, lambda p=p: self.answer(p["name"], p.get("dir") or p["name"]),
                        size=12).pack(anchor="w", pady=2)
        more = f"{len(hits) - MAX_HINTS} more - keep typing. " if len(hits) > MAX_HINTS else ""
        if can_add(hits, text):
            self.button(self.hints, f"Add new person: {text}", PINK, self.typed, size=12).pack(anchor="w", pady=2)
        elif not any(core(p["name"]).lower() == text.lower() for p in hits):
            more += "Click one of these, or type their full name."
        if more:
            self.label(self.hints, more, GREY, 12).pack(anchor="w")
        self.root.after(20, self.fit)

    def fit(self):
        """Move up (never down) so the bottom buttons stay above the taskbar as the list grows."""
        self.root.update_idletasks()
        y, h, sh = self.root.winfo_y(), self.root.winfo_height(), self.root.winfo_screenheight()
        if y + h > sh - 80:
            self.root.geometry(f"+{self.root.winfo_x()}+{max(0, sh - 80 - h)}")

    def typed(self):
        text = " ".join(self.entry.get().split())
        if not text:
            return
        hits = matches(self.q.get("people") or [], text)
        exact = [p for p in hits if core(p["name"]).lower() == text.lower() or
                 any(a.lower() == text.lower() for a in p.get("aliases") or [])]
        if exact:
            p = exact[0]
            return self.answer(p["name"], p.get("dir") or p["name"])
        if not can_add(hits, text):
            return  # one word that is part of known names ("Clancy"): pick one, never guess
        if self.q.get("kb"):
            add_person(self.q["kb"], text)
        self.answer(text, text)

    def key(self, n):
        if self.root.focus_get() is getattr(self, "entry", None):
            return  # typing a name, not choosing a button
        if 0 < n <= len(self.people_keys):
            p = self.people_keys[n - 1]
            self.answer(p["name"], p.get("dir") or p["name"])

    def answer(self, name, folder):
        q = self.q
        enrolled, why = ("", "")
        if name != "unknown":
            try:
                enrolled, why = enroll(q, name, folder)
            except Exception as e:
                why = f"the picture could not be saved ({e})"
        write_answer(self.d, q, name, enrolled)
        if name == "unknown":
            msg = "OK - becky will not ask about this one again."
        elif q.get("speaker"):
            msg = f"Saved: {name} is the one talking."
        elif enrolled:
            msg = f"Saved: {name}. becky will recognise them next time."
        else:
            msg = f"Saved: {name} ({why})."
        self.status.config(text=msg)
        self.i += 1
        self.root.after(1300, self.show)

    def play(self):
        q = self.q
        start = max(0.0, float(q.get("at", 0)) - 3)
        if self.ffplay and os.path.exists(self.ffplay):
            subprocess.Popen([self.ffplay, "-hide_banner", "-loglevel", "error", "-autoexit", "-ss", f"{start:.2f}",
                              "-t", "6", "-x", "960", "-window_title", "becky - this moment", q["video"]],
                             creationflags=0x08000000)
        else:
            os.startfile(q["video"])


def selftest():
    people = [{"name": "John Anthony Clancy", "aliases": ["TakingBack2007"]},
              {"name": "Shelby Alyse Clancy (née Kerberg)", "aliases": ["Shelby Kerberg", "PopTart Barbie"]},
              {"name": "Hair Jordan"}]
    assert [p["name"] for p in matches(people, "clancy")] == ["John Anthony Clancy", "Shelby Alyse Clancy (née Kerberg)"], \
        "a last name shows everyone with it, never picks one"
    assert [p["name"] for p in matches(people, "poptart")] == ["Shelby Alyse Clancy (née Kerberg)"]
    assert [p["name"] for p in matches(people, "sh")][0].startswith("Shelby")
    assert matches(people, "x") == []
    assert not can_add(matches(people, "clancy"), "clancy"), "a bare last name can never become a new person"
    assert can_add(matches(people, "clancy"), "Jake Clancy") and can_add([], "Braxton")
    assert not can_add(matches(people, "hair jordan"), "Hair Jordan")
    assert clock(83.4) == "1:23" and clock(3725) == "1:02:05"
    assert safe_dir('A: "B"?') == "A B"
    print("selftest ok")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir")
    ap.add_argument("--ffplay", default="")
    ap.add_argument("--selftest", action="store_true")
    a = ap.parse_args()
    if a.selftest:
        return selftest()
    alive = os.path.join(a.dir, "popup.alive")
    if os.path.exists(alive) and datetime.datetime.now().timestamp() - os.path.getmtime(alive) < 8:
        return  # another window is already open; it picks new questions up itself
    if not pending(a.dir):
        return
    try:
        import ctypes
        ctypes.windll.shcore.SetProcessDpiAwareness(1)  # sharp text on a scaled screen
    except Exception:
        pass
    Window(a.dir, a.ffplay).root.mainloop()


if __name__ == "__main__":
    sys.exit(main())
