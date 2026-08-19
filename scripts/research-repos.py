#!/usr/bin/env python3
"""Read the reference video-editing repos and write one research note per repo.

Why this exists: `shorts-user-feedback.md` lists 21 projects and asks what STEP
each one runs that becky does not. That is bulk reading, not judgement, so it
runs on FREE models and costs no Anthropic budget. The judgement half - "build
it, or declare why we don't need it" - stays with the orchestrator.

Resumable: a repo whose note already exists is skipped, so a rate-limit or a
killed process loses at most one repo.

Free-only is not a preference here, it is the rule (CLAUDE.md): the model list
is derived from OpenRouter's own catalogue filtered to `:free`, never
hand-written, because a fabricated model id is a silent 404 that degrades to
nothing.
"""

import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT_DIR = os.path.join(ROOT, "research")
LOG = os.path.join(ROOT, "research", "_repo-research.log")

REPOS = [
    "FujiwaraChoki/supoclip",
    "sonnhfit/pavo-engine-py",
    "mifi/editly",
    "alexandremendoncaalvaro/CorridorKey-Runtime",
    "JeremySNR/clip-forge",
    "SeanSpon/clipbot",
    "RshieRish/post-fast-main",
    "atherion005-byte/agent-opus",
    "fralapo/clippyme",
    "Anil-matcha/AI-Youtube-Shorts-Generator",
    "tdollar15/OpusClip-Clone",
    "louisedesadeleer/clipify",
    "waterboxdeveloper/CliperAi-",
    "JerielCodes/clipsai2",
    "redpanda12138/video-copywriting-style-learner",
    "PeachOrange/persona-alignment-rewrite",
    "aeronesto/video-editor-app",
    "modelscope/FunClip",
    "modelscope/FunASR",
    "antiboredom/videogrep",
    "shreesha345/AI-short-creator",
    "msnodderly/vcut",
]

# Files worth reading, best first. A repo's pipeline lives in its entry points
# and its prompt/config files, not in its tests or its lockfiles.
CODE_EXT = (".py", ".js", ".ts", ".tsx", ".jsx", ".go", ".rs", ".toml", ".yaml", ".yml")
INTERESTING = re.compile(
    r"(main|app|pipeline|process|clip|short|edit|render|cut|caption|subtitle|"
    r"transcri|crop|face|track|speak|detect|score|rank|prompt|agent|llm|"
    r"analy|highlight|moment|segment|viral|compose)",
    re.I,
)
SKIP = re.compile(r"(node_modules|/test|test/|\.test\.|spec\.|__pycache__|/dist/|/build/|migrations|\.lock)", re.I)

# Verified-clean model ids, best first. See free_models().
PREFERRED = [
    "nvidia/nemotron-3-ultra-550b-a55b:free",   # 1M context, clean markdown
    "nvidia/nemotron-3-super-120b-a12b:free",   # 262k context, clean markdown
    "google/gemma-4-31b-it:free",               # rate-limits often, but honest output
]

# A note must carry most of the sections it was asked for. Anything less is a
# model that wandered off, and writing it to disk is worse than writing nothing:
# it looks like research.
REQUIRED_HEADINGS = 6

MAX_FILE_BYTES = 14000
MAX_TOTAL_BYTES = 110000
MAX_FILES = 14


def log(msg):
    line = f"[{time.strftime('%H:%M:%S')}] {msg}"
    print(line, flush=True)
    with open(LOG, "a", encoding="utf-8") as fh:
        fh.write(line + "\n")


def gh_token():
    for var in ("GITHUB_TOKEN", "GH_TOKEN"):
        if os.environ.get(var):
            return os.environ[var]
    try:
        return subprocess.run(
            ["gh", "auth", "token"], capture_output=True, text=True, timeout=20
        ).stdout.strip()
    except Exception:
        return ""


def gh_get(url, token, raw=False):
    req = urllib.request.Request(url)
    req.add_header("Accept", "application/vnd.github.raw" if raw else "application/vnd.github+json")
    req.add_header("User-Agent", "becky-research")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(req, timeout=60) as resp:
        data = resp.read()
    return data if raw else json.loads(data)


def free_models(key):
    """Ask OpenRouter which :free ids exist RIGHT NOW, biggest context first.

    Never hardcode this list. Two ids in an earlier hand-written rotation did
    not exist on OpenRouter at all, so two of five fallbacks were guaranteed
    404s and the feature silently degraded.
    """
    req = urllib.request.Request("https://openrouter.ai/api/v1/models")
    req.add_header("Authorization", "Bearer " + key)
    with urllib.request.urlopen(req, timeout=60) as resp:
        cat = json.loads(resp.read())
    live = set()
    ctxof = {}
    for m in cat.get("data", []):
        mid = m.get("id", "")
        if not mid.endswith(":free"):
            continue
        ctx = m.get("context_length") or 0
        if ctx < 60000:
            continue  # we send ~100KB of source; a small window just truncates
        live.add(mid)
        ctxof[mid] = ctx
    # PREFERRED are ids MEASURED to return clean structured markdown (probe,
    # 2026-08-19). This is a preference over the live catalogue, never a
    # replacement for it - an id that has since vanished is simply dropped.
    #
    # The three left out are not slow, they are WRONG, and each failed silently:
    #   nemotron-3.5-lightning  writes its chain of thought into `content`, so
    #                           every note it produced was a thinking transcript
    #                           with zero headings. Four files shipped like that.
    #   dots-3-note-preview     returns empty content (all of it in `reasoning`).
    #   laguna-s-2.1            ignored the instruction and asked for clarification.
    ordered = [m for m in PREFERRED if m in live]
    rest = sorted((m for m in live if m not in PREFERRED), key=lambda m: -ctxof[m])
    return ordered + rest


PROMPT = """You are reading an open-source video-editing / short-form-clip project so that a
different, more advanced pipeline can learn from its PROCESS.

Write a factual engineering note in Markdown. Report only what the source actually shows.
If something is not in the files you were given, write "not visible in the files read" -
never guess, never pad, never write marketing copy.

Use exactly these sections:

## What it is
Two or three sentences. What does it take in, what does it emit.

## The pipeline, in order
A numbered list of the ACTUAL stages in the code, in execution order. For each stage name the
function or file that implements it. This is the most important section - be concrete.

## Models, libraries and services
A table: what it uses, for which stage, and whether it is local or a paid API.

## Prompts
Quote verbatim any LLM prompt found in the source, in a fenced block. If there are none, say so.
This is high value - do not summarise a prompt, quote it.

## How it decides WHAT to clip
The actual selection/ranking logic. Thresholds, scores, heuristics, or "it asks an LLM and
trusts the answer". Name the file.

## How it decides framing / cropping
Same treatment. If it does not reframe, say so plainly.

## Multi-pass or iteration
Does anything run more than once, re-check its own output, or refine? Most of these tools do
NOT - if so, say so, because that is itself the finding.

## Steps here that a transcript-first clipper would MISS
Bullet list. Be specific and mechanical (e.g. "snaps cut points to the nearest silence
boundary detected by X", not "has good pacing").

## Worth stealing
Bullets, ranked. Concrete: a file, a function, a constant, a prompt, an ordering. Include the
licence if you saw one. If nothing here is worth stealing, say that outright - a clear "no"
is more useful than a padded list.

REPO: {repo}
DESCRIPTION: {desc}
LICENCE: {lic}

FILES:
{files}
"""


def call_model(key, model, prompt):
    body = json.dumps(
        {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": 8000,
            "temperature": 0.2,
        }
    ).encode()
    req = urllib.request.Request("https://openrouter.ai/api/v1/chat/completions", data=body)
    req.add_header("Authorization", "Bearer " + key)
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=300) as resp:
        out = json.loads(resp.read())
    if out.get("error"):
        raise RuntimeError(out["error"].get("message", "unknown"))
    return out["choices"][0]["message"].get("content") or ""


def usable(text):
    """A note is usable only if it actually has the sections it was asked for.

    Without this check the runner happily wrote 24KB of a model's internal
    monologue to research/ four times over, and the files looked plausible from
    the outside - right size, right filename, no error anywhere.
    """
    if not text:
        return None
    # Some models prepend a preamble before the real answer. Keep from the first
    # real heading onward; drop anything before it.
    i = text.find("## What it is")
    if i > 0:
        text = text[i:]
    if sum(1 for ln in text.split(chr(10)) if ln.startswith("## ")) < REQUIRED_HEADINGS:
        return None
    return text.strip()


def gather(repo, token):
    owner, name = repo.split("/")
    meta = gh_get(f"https://api.github.com/repos/{owner}/{name}", token)
    branch = meta.get("default_branch", "main")
    lic = (meta.get("license") or {}).get("spdx_id") or "not stated"
    desc = meta.get("description") or ""

    tree = gh_get(
        f"https://api.github.com/repos/{owner}/{name}/git/trees/{branch}?recursive=1", token
    )
    paths = [n["path"] for n in tree.get("tree", []) if n.get("type") == "blob"]

    picked = [p for p in paths if re.search(r"readme", p, re.I) and p.count("/") == 0][:1]
    scored = []
    for p in paths:
        if p in picked or SKIP.search(p) or not p.endswith(CODE_EXT):
            continue
        s = 2 if INTERESTING.search(p) else 0
        s += 1 if p.count("/") <= 1 else 0
        if s:
            scored.append((s, -len(p), p))
    scored.sort(reverse=True)
    picked += [p for _, _, p in scored[: MAX_FILES - len(picked)]]

    blob, total = [], 0
    for p in picked:
        try:
            raw = gh_get(
                f"https://api.github.com/repos/{owner}/{name}/contents/{p}?ref={branch}",
                token,
                raw=True,
            ).decode("utf-8", "replace")
        except Exception as e:
            log(f"  skip {p}: {e}")
            continue
        raw = raw[:MAX_FILE_BYTES]
        if total + len(raw) > MAX_TOTAL_BYTES:
            break
        total += len(raw)
        blob.append(f"----- FILE: {p} -----\n{raw}\n")
    return desc, lic, "\n".join(blob)


def slug(repo):
    return "repo-" + repo.split("/")[1].strip("-").lower().replace(".", "-")


def main():
    key = os.environ.get("OPENROUTER_API_KEY", "").strip()
    if not key:
        log("FATAL: OPENROUTER_API_KEY not set")
        return 1
    token = gh_token()
    models = free_models(key)
    if not models:
        log("FATAL: no free models with a usable context window")
        return 1
    log(f"free models available: {len(models)} -> {models[:4]}")

    for repo in REPOS:
        out = os.path.join(OUT_DIR, slug(repo) + ".md")
        if os.path.exists(out) and os.path.getsize(out) > 800:
            log(f"skip (done): {repo}")
            continue
        log(f"reading {repo}")
        try:
            desc, lic, files = gather(repo, token)
        except Exception as e:
            log(f"  FETCH FAILED {repo}: {e}")
            continue
        if not files.strip():
            log(f"  no readable source in {repo}")
            continue
        prompt = PROMPT.format(repo=repo, desc=desc, lic=lic, files=files)

        text = None
        for model in models[:6]:
            try:
                log(f"  -> {model}")
                text = usable(call_model(key, model, prompt))
                if text and len(text) > 600:
                    break
                log("  answer had no usable structure, next model")
                text = None
            except Exception as e:
                log(f"  {model} failed: {e}")
                time.sleep(4)
        if not text:
            log(f"  ALL MODELS FAILED for {repo}")
            continue

        header = (
            f"# {repo}\n\n"
            f"Source: https://github.com/{repo} | licence: {lic}\n\n"
            f"Read for `shorts-user-feedback.md`: what step does this run that becky does not?\n"
            f"Written by a free model reading the source; the build/skip judgement is not its call.\n\n"
            f"---\n\n"
        )
        with open(out, "w", encoding="utf-8") as fh:
            fh.write(header + text.strip() + "\n")
        log(f"  WROTE {os.path.relpath(out, ROOT)} ({len(text)} chars)")
        time.sleep(2)

    log("done")
    return 0


if __name__ == "__main__":
    sys.exit(main())
