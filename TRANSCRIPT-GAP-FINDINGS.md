# Transcript Gap: becky ASR vs human burned-in captions

R&D scope note (Fact-Forcing Gate): nothing imports this markdown file; no existing file
serves this purpose (Glob for `*GAP*` returned nothing); it reads/writes no data file and
contains only analysis text plus tool output. Per instruction: "if we need new tools, spec
them out. if we just need a workflow of already existing tools chained together properly,
make it happen."

Method: `ffmpeg -vf fps=1` → `becky-ocr` (human caption = OCR ground truth) vs
`becky-transcribe --format srt` (ASR). 5 single-speaker social clips. Token-level WER is
inflated because OCR of word-by-word animated captions sampled at 1 fps fragments lines and
picks up on-screen graphics + emphasis-styled artifacts; read it as "noise ceiling," not
true ASR error. Where the caption text is legible, **ASR is consistently MORE complete and
cleaner than the OCR'd fragments.**

## Per clip

**evil_billionaire2** (human ~614 ocr-words / asr 745 / raw WER 42%)
- OCR badly fragmented (animation + emphasis caps like "MILLION", "OWOR" artifacts).
- ASR is full, grammatical, captures asides OCR missed ("you got problems", "Where is that guy?").
- Diff = OCR noise + censoring (`sh*t`/`f**k` in caption vs `shit`/`f`/`fing` in ASR). No content miss by ASR.

**forgot_name_3_times** (259 / 318 / WER 34%)
- Cleanest pair. ASR matches caption sentence-for-sentence; even nails the name gag.
- Caption: "she f***ing yelled" — ASR: "she fing yelled". Pure profanity-censor + casing/punctuation. CONTENT match.

**grammy2** (330 / 269 / WER 51%)
- High WER is an artifact: OCR ate the album-art graphic ("PARENTAL ADVISORY EXPLICIT CONTENT",
  "FOR YOUR GRAMMY CONSIDERATION", "Reply to ...'s comment") and reply-overlay UI — none of which are captions.
- ASR cleanly ignores on-screen graphics and transcribes only speech. ASR is the better transcript here.

**hot_girl_toddler** (238 / 245 / WER 35%)
- ASR full and clean. OCR fragments ("HUT", "BUT INE", "OITTIW") are partial word-reveals.
- ASR fills clauses OCR dropped ("And if you've never had a kid", "you don't know it, but I know it"). CONTENT match; ASR ahead.

**i_dont_need_a_man** (296 / 358 / WER 39%)
- ASR more complete; recovers stutters/repeats the caption compressed ("contributing anything, contributing anything").
- Caption "plug fell out" vs ASR "plug fill up plug fell out" = ASR over-captured a disfluency. Casing/punctuation differ.

## Synthesis: classify the gaps

- **CONTENT (ASR missing/wrong words):** Minimal. ASR is at parity or ahead of the legible
  caption on actual words in every clip. The apparent word-count gaps come from (a) OCR
  fragmentation/artifacts and (b) ASR correctly *including* speech the human caption tightened
  or the OCR sampling missed. Residual true errors are minor proper-noun/brand spellings
  ("Johnny Gilbert" vs the creator's intended spelling, "Hoggir"/"hot girl") — small, deterministic-fixable with a name dictionary.
- **FORMATTING (the real gap):** This is where 100% of the meaningful delta lives.
  1. **Profanity censoring** — captions write `f**k`, `sh*t`, `f***ing`; ASR emits `fuck`/`shit`
     or mangled `f`/`fing`/`fk`. Deterministic substitution.
  2. **Casing & punctuation** — ASR sentence-cases + punctuates reasonably already; captions
     follow their own emphasis casing.
  3. **Line breaks / segment length / timing** — ASR ships ~3-4s segments split mid-sentence
     ("That looks" / "sick"). Human captions are 1-4 word animated chunks, centered, styled.
     This is a *chunking/timing reflow* problem, not transcription.
- **SOUND-EFFECTS / NON-SPEECH:** **None present in these 5 clips.** No `[music]`, `*sigh*`,
  `*CHOKES ON NOTHING*` in any human caption. The bracket/all-caps "hits" were on-screen
  graphics (album art, reply-overlay UI) and emphasis-styled words, not SFX cues. So for this
  content style, an audio sound-event tagger is **not** required. (If a future clip set uses
  meme SFX captions, that becomes a separate, genuinely new capability — ASR can never emit it.)

## Can existing tools close it?

**Yes — workflow, not new tool.** ASR content is already good enough; the gap is formatting.
`becky-transcribe` + a **deterministic post-processor** closes the large majority:
- profanity-censor map (`fuck`→`f**k`, `shit`→`sh*t`, etc.)
- proper-noun/brand spelling dictionary
- punctuation/casing normalize (mostly already done by ASR)
- caption-reflow: re-chunk the existing word timings into N-word display lines (use SRT timings becky already emits)

A constrained local-LLM caption-formatter is **optional polish**, not required — and if used it
**must be hard-constrained to reorder/segment/censor only, never invent or replace words**
(ASR is already at/above caption content fidelity, so any rewriting is pure downside risk).

## Recommendation (<=6 bullets)

- Do **not** build an audio sound-event tagger for this content; no SFX cues exist in the sample set. Revisit only if a clip set actually uses them.
- Build a small **deterministic caption post-processor** chained after `becky-transcribe`: profanity-censor map + name/brand dictionary + casing/punct normalize.
- Add a **caption-reflow step** that re-chunks the SRT into 1-4 word styled display lines using the timings becky already produces — this is the biggest perceived "human" difference.
- Treat ASR output as the content source of truth; the human caption is *stylistically* different, not more accurate.
- Keep OCR-of-captions only as an eval harness (ground-truth-ish), and dedupe/strip on-screen graphics + reply-overlay UI before scoring, or WER stays artificially high.
- Defer any LLM formatter; if added, constrain it to segment/censor/case only with a no-new-words guardrail and diff-check against ASR tokens.
