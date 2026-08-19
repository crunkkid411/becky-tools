# The iPhone archive, swept for AI + video

`shorts-user-feedback.md` asks: go through
`C:\Users\only1\Documents\Obsidian\browser_data\iPhone`, find anything about AI and video, and
say whether it is useful. 1,770 pages. Swept 2026-08-19.

## Read this first — the shape of the archive

**YouTube pages in this archive contain NO transcript and NO description body.** They captured the
page chrome only: "About / Press / Copyright / Contact us / Creators…", `word_count: 26`,
`confidence: 0.350`. Both of the editing-craft videos Jordan saved —
*"How to Edit Hours of Video Footage"* and *"This 'Boring' Editing Exercise Will Save You 5 YEARS"* —
are empty in exactly this way. Their **titles and URLs are the only signal**, and the titles are
worth something on their own (see below), but nobody should go looking for the content: it is not
there.

Only article, arXiv, GitHub and docs pages carry real text. Filtering on the frontmatter's own
`word_count >= 300` plus a video/AI keyword leaves **698 pages** out of 1,770 — that is the set
worth reading, and the rest is noise.

## The one finding that changes how we build

### F-16: "Improving LLM Video Understanding with 16 Frames Per Second" (arXiv 2503.13956)

This is published evidence for the rule Jordan has been repeating, and it is the most useful thing
in the archive:

> "existing methods primarily rely on static features extracted from images sampled at a fixed low
> frame rate of FPS 2, leading to critical visual information loss… videos contain both slowly
> changing elements, such as backgrounds and scenes, and rapidly changing, fleeting details, such
> as **body movements and micro-expressions**… low-frame-rate sampling like 1 FPS may be sufficient
> for capturing a video's overall theme and context, [but] **it fails to preserve rapidly changing
> visual cues**."

That is his complaint stated by a paper: *"You continue to give me tools and analysis at less than
this frame rate and we still are UNABLE to use them because of it."*

What they measured: at 16 FPS a **7B** model beats **GPT-4o and Gemini-1.5-Pro** on fine-grained
temporal tasks (high-speed sports — basketball, football, gymnastics, diving) and takes SOTA on
TemporalBench and MotionBench among 7B video LLMs. Frame rate bought more than model size did.

**Why this matters for the shorts pipeline specifically.** His own worked example is the rubber-snake
prank: *"The framing must be on Robby's face 1 - 3 frames BEFORE he realizes what is happening so
that the dramatic change in his expression is portrayed to the audience; the payoff."* A 1-3 frame
window is 33-100ms. Sampled at 2 FPS the model sees one frame per 500ms — **the entire punchline
falls between two samples.** No prompt, no bigger model and no second pass recovers a moment that
was never sampled. This is a sampling failure, not a reasoning failure.

**The design rule it gives us**, and it is cheap: do not sample a VL uniformly across a clip.
Use the cheap deterministic signals we already have (audio spikes and pitch rises from
`internal/audiosig`, motion, face-track discontinuities) to LOCATE the candidate moment, then hand
the VL a **dense burst of consecutive frames** around it. Dense where it matters, sparse everywhere
else — which is also what F-16's own variable-frame-rate decoding does. We already track faces at
every frame (852 samples over 28.4s, verified); the gap is that nothing feeds a dense burst to the
vision model.

They also compress visual tokens within each 1-second clip via a 3-layer MLP aligner, because 16 FPS
otherwise explodes the token sequence. We are not training anything, so the transferable part is the
sampling strategy, not the architecture.

## Titles worth acting on even though the pages are empty

Both are Jordan's own saved editing references, and the titles alone say what he values:

- *"How to Edit Hours of Video Footage — Timeline Organization Basics"* (DaVinci/Ground Control) —
  the problem he actually has: hours in, a short out.
- *"This 'Boring' Editing Exercise Will Save You 5 YEARS (But Most Ignore It)"* (Austen Menges).

If we want the content, it has to be fetched fresh from the URLs in the frontmatter; the archive
cannot supply it.

## Pages worth reading next, ranked

Not yet read. Listed so the next pass does not re-derive the shortlist.

| Page | Why |
|---|---|
| `DaVinci Resolve Scripting API Doc v21.0` (10.7k words) | The scripting surface of a real NLE — closest thing to a spec for "drive an editor from code" |
| `Building a VideoAgent-Style Multi-Agent System: Intent Parsing, Graph Planning, and Tools` (5.1k) | Process, which is what we were told to look for; complements `research/videoagent-integration.md` |
| `anyc/avcut — Frame-accurate video cutting with only small quality loss` | Directly relevant to rendering jumpcuts without a full re-encode |
| `Best Face Tracking SDKs for Real-Time Video Conferencing in 2026` | Our stage-4 problem, surveyed |
| `I tested Gemma 4, Qwen 3.5 and Ministral 3 for vision tasks and only one…` | A live comparison including our own default VL |
| `Blaizzy/mlx-vlm`, `wenzhengzeng/D2VLM-Models`, `SenseNova-Vision-Corpus-50M` | VL options and data |
| `HKUDS/VideoAgent`, `HKUDS/ViMax` | Agentic video pipelines |
| `mifi/lossless-cut` | Same author as `editly`, already in the 21-repo pass |
| `Eddie AI`, `Wideframe`, `OpusClip`, `ClipWith`, `FrameCompose`, `AI Video Cut Reviews in 2026` | The competitors he says all fall short — useful as a list of what NOT to reproduce |

## Honest assessment

One finding here is genuinely load-bearing (F-16 / dense sampling) and it is actionable now.
The rest is a reading list. The editing-craft videos — the thing most likely to encode HIS taste,
which is what the pipeline is actually missing — did not survive the clipping, and that is the
disappointment of this sweep.
