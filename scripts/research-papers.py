#!/usr/bin/env python3
"""Read the four NEW sources in `shorts-user-feedback.md` (the UPDATE section).

Two are arXiv papers, two are Hugging Face model cards - not GitHub repos - so
`research-repos.py` cannot fetch them. Everything else is reused from it:
the live `:free` model list, the call, and the anti-chain-of-thought validator
that already caught four fake notes.

Resumable and free-only, same as its sibling.
"""

import importlib.util
import json
import os
import re
import sys
import time
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT_DIR = os.path.join(ROOT, "research")

# research-repos.py has a hyphen, so it cannot be imported by name.
_spec = importlib.util.spec_from_file_location(
    "research_repos", os.path.join(os.path.dirname(os.path.abspath(__file__)), "research-repos.py")
)
rr = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(rr)

SOURCES = [
    ("paper-2509.10761", "https://arxiv.org/abs/2509.10761", "arxiv"),
    ("paper-2512.14698", "https://arxiv.org/abs/2512.14698", "arxiv"),
    ("model-aero-realtime-4b", "kcz358/aero-realtime-4B", "hf"),
    ("model-marlin-2b", "NemoStation/Marlin-2B", "hf"),
]

MAX_CHARS = 90000

PROMPT = """You are reading a research paper / model card so that an existing offline video-editing
pipeline ("becky") can decide whether to adopt anything from it.

becky already does, locally and offline on one 8GB RTX 3070 laptop GPU:
word-level ASR (Parakeet), face detection + tracking (InsightFace / MediaPipe), active-speaker
detection (LR-ASD), shot-cut detection, silence/energy audio signals, a vision-language pass
(Gemma-4 E4B escalating to 12B), moment ranking, 9:16 reframing, and burned-in captions.
ONLY ONE vision model fits in 8GB at a time.

Write a factual engineering note in Markdown. Report only what the source actually states.
If something is not in the text you were given, write "not stated in the source" - never guess,
never pad, never write marketing copy. Quote numbers verbatim when the source gives them.

Use exactly these sections:

## What it is
Two or three sentences. What problem, what input, what output.

## The method, in order
A numbered list of the ACTUAL stages the source describes, in order. Name the component the
source names for each stage. Be concrete.

## Numbers the source reports
A table: metric, value, on what benchmark/dataset. Verbatim. If none, say so.

## Prompts, losses or decision rules
Quote verbatim any prompt, scoring rule, loss or threshold the source gives, in a fenced block.
If there are none, say so.

## Hardware and runtime cost
Parameters, VRAM, latency, throughput - whatever the source states. Say plainly whether this
could run alongside or instead of a single 8GB-budget local vision model.

## What becky does NOT do that this does
Bullet list. Specific and mechanical. This is the most valuable section.

## Worth adopting
Ranked bullets. For each: what exactly, and what it would replace or add in the pipeline above.
If nothing here is worth adopting, say that outright - a clear "no" is more useful than a padded
list. Include the licence if the source states one.

SOURCE: {name} ({url})

TEXT:
{text}
"""


def fetch(url):
    req = urllib.request.Request(url)
    req.add_header("User-Agent", "Mozilla/5.0 becky-research")
    with urllib.request.urlopen(req, timeout=90) as resp:
        return resp.read().decode("utf-8", "replace")


def detag(html):
    html = re.sub(r"(?is)<(script|style|nav|footer|svg)[^>]*>.*?</\1>", " ", html)
    html = re.sub(r"(?s)<[^>]+>", " ", html)
    html = (
        html.replace("&amp;", "&").replace("&lt;", "<").replace("&gt;", ">")
        .replace("&quot;", '"').replace("&#39;", "'").replace("&nbsp;", " ")
    )
    return re.sub(r"[ \t]{2,}", " ", re.sub(r"\n{3,}", "\n\n", html)).strip()


def get_arxiv(url):
    """Prefer the HTML full text; fall back to the abstract page.

    The full text is what makes the note worth writing - an abstract alone
    produces a note that says "not stated in the source" eight times.
    """
    aid = url.rstrip("/").split("/")[-1]
    for cand in (f"https://arxiv.org/html/{aid}v3", f"https://arxiv.org/html/{aid}v2",
                 f"https://arxiv.org/html/{aid}v1", f"https://arxiv.org/abs/{aid}"):
        try:
            txt = detag(fetch(cand))
        except Exception:
            continue
        if len(txt) > 3000:
            return txt[:MAX_CHARS]
    raise RuntimeError("no readable arxiv text")


def get_hf(repo):
    """Model card + the file list. The card is the claim, the files are the proof."""
    parts = []
    try:
        card = fetch(f"https://huggingface.co/{repo}/raw/main/README.md")
        parts.append("----- README.md -----\n" + card)
    except Exception as e:
        parts.append(f"(README.md unreadable: {e})")
    try:
        info = json.loads(fetch(f"https://huggingface.co/api/models/{repo}"))
        files = [s.get("rfilename", "") for s in info.get("siblings", [])]
        parts.append(
            "----- HUB METADATA -----\n"
            + json.dumps(
                {
                    "pipeline_tag": info.get("pipeline_tag"),
                    "tags": info.get("tags"),
                    "license": (info.get("cardData") or {}).get("license"),
                    "downloads": info.get("downloads"),
                    "lastModified": info.get("lastModified"),
                    "files": files[:80],
                },
                indent=1,
            )
        )
        for want in ("config.json", "preprocessor_config.json", "generation_config.json"):
            if want in files:
                try:
                    parts.append(
                        f"----- {want} -----\n"
                        + fetch(f"https://huggingface.co/{repo}/raw/main/{want}")[:6000]
                    )
                except Exception:
                    pass
    except Exception as e:
        parts.append(f"(hub metadata unreadable: {e})")
    return "\n\n".join(parts)[:MAX_CHARS]


def main():
    key = os.environ.get("OPENROUTER_API_KEY", "").strip()
    if not key:
        rr.log("FATAL: OPENROUTER_API_KEY not set")
        return 1
    models = rr.free_models(key)
    if not models:
        rr.log("FATAL: no free models with a usable context window")
        return 1
    rr.log(f"papers: {len(models)} free models -> {models[:4]}")

    for slug, ref, kind in SOURCES:
        out = os.path.join(OUT_DIR, slug + ".md")
        if os.path.exists(out) and os.path.getsize(out) > 800:
            rr.log(f"skip (done): {slug}")
            continue
        rr.log(f"reading {slug} ({ref})")
        try:
            text = get_arxiv(ref) if kind == "arxiv" else get_hf(ref)
        except Exception as e:
            rr.log(f"  FETCH FAILED {slug}: {e}")
            continue
        if len(text) < 500:
            rr.log(f"  too little text for {slug} ({len(text)} chars)")
            continue
        url = ref if kind == "arxiv" else f"https://huggingface.co/{ref}"
        prompt = PROMPT.format(name=slug, url=url, text=text)

        note = None
        for model in models[:6]:
            try:
                rr.log(f"  -> {model}")
                raw = rr.call_model(key, model, prompt)
                i = raw.find("## What it is")
                if i > 0:
                    raw = raw[i:]
                note = rr.usable(raw)
                if note and len(note) > 600:
                    break
                rr.log("  answer had no usable structure, next model")
                note = None
            except Exception as e:
                rr.log(f"  {model} failed: {e}")
                time.sleep(4)
        if not note:
            rr.log(f"  ALL MODELS FAILED for {slug}")
            continue

        header = (
            f"# {slug}\n\n"
            f"Source: {url}\n\n"
            f"Read for `shorts-user-feedback.md` (UPDATE section): what does this offer becky?\n"
            f"Written by a free model reading the source; the adopt/skip judgement is not its call.\n\n"
            f"---\n\n"
        )
        with open(out, "w", encoding="utf-8") as fh:
            fh.write(header + note.strip() + "\n")
        rr.log(f"  WROTE research/{slug}.md ({len(note)} chars)")
        time.sleep(2)

    rr.log("papers done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
