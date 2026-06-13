# Forensic Output Philosophy — how becky tools (and the agents using them) must report findings

Authored 2026-06-07 from Jordan's correction of the `physical-interactions-summary.md` output.
**This governs EVERY tool that produces a human-facing finding** (becky-validate, becky-review,
becky-events, becky-identify, the orchestrator) **and every future tool or agent added to the
set.** Nuanced specifics are everything. If a finding isn't CLEAR, it gets overlooked — and an
overlooked observation is lost evidence. This is a recall requirement, not a style preference.

## TOP PRINCIPLE (added 2026-06-08 per Jordan): BE ACCURATELY CONFIDENT — CORROBORATE, THEN CONCLUDE.
The job of becky-tools is NOT to dump a pile of "maybes" for a human to sort — if a human has to
sort mis-flagged clips, the tool has failed. The job is to **combine multiple data points across
multiple passes, score confidence, and reach a CONFIDENT, CORROBORATED CONCLUSION** — then state
it plainly. The human reviews the confident findings; they do not do the tool's sorting.
- **One weak signal → "unknown" / candidate.** (A lone 0.50 face match is NOT an identification.)
- **Multiple independent signals agreeing → CONCLUDE and SAY IT.** Voice + face + context all point
  to Shelby → write **"Shelby"**, not "possibly Shelby (paragraph of hedging)". Fixtures + a vlog +
  a listing all match → **"this is 2601 Chatham Cir"**, with the corroborating points attached —
  not "a human must decide if this is the address."
- The corroboration IS the precision. Casting wide (recall) at the DETECTION layer is right; but the
  OUTPUT must be the corroborated conclusion, with its evidence and a confidence score — not the raw
  candidates. Aim for dead-accurate nuanced finds. An occasional error is acceptable; a flood of
  unsorted maybes is not.
- This SUPERSEDES the earlier "never concludes / candidate-not-conclusion" framing below where they
  conflict: don't conclude from ONE thin signal, but DO conclude — confidently, by name, with the
  address — when the data points corroborate. Surface breakthroughs loudly.

## The one rule: SAY WHAT YOU KNOW, PLAINLY. FLAG WHAT YOU DON'T.
Two failure modes, both fatal:
- **Hiding a known thing** behind hedge-words, codes, or jargon — "speaker_1_dark_hair_dark_beard",
  "hand contact was observed", "contact with the iliac crest". Reads like the tool is broken or
  unsure → the reader dismisses it.
- **Asserting an uncertain thing** as fact. Reads as over-claiming → discredits the whole report.
Same discipline fixes both: separate KNOWN from UNCERTAIN, then for each, be CLEAR.

## 1. Name what you know. Don't anonymize established facts.
- If identity is established, write the NAME: **"John Clancy,"** not "speaker_1_dark_hair_dark_beard."
  A coded label implies recognition failed. If we KNOW it's John, say John.
- When identity is genuinely a guess, say so plainly: "an unidentified man (candidate: John
  Clancy, voice match 0.71)." Clear about what's known vs guessed — not a sterile code.
- Same logic for ACTIONS. If we know what happened, describe what happened — don't reduce it to
  mechanics. "He tapped her butt, then gripped her hips," not "hand movement was detected."

## 2. Plain human words — NOT clinical jargon.
- Say **butt, hips, waist, thigh, chest, crotch** — the words a detective, a juror, and any
  normal person use. "Iliac crest / lateral waist area / upper lateral thigh region" is nonsense
  to a reasonable human and gets skipped. Latin anatomy is just a fancier way of avoiding the
  plain word — still avoidance, and worse, because it's also unclear.
- **Correction of earlier tuning (own it):** a prior auto-research pass pushed becky-validate
  toward anatomical precision to stop the model softening contact. That OVERSHOT — it produced
  jargon AND still missed the act. The real fix for softening is PLAIN, DIRECT, COMPLETE
  language, never medical vocabulary. See `AUTORESEARCH-SPEC.md` (now annotated) — this doc wins.

## 3. Describe the ACT and its dynamics — not just the motion.
"Hands moved" is not "he forcefully pulled her into his body while she was trying to stop him."
The evidence lives in the dynamics a human can see:
- WHO did it to WHOM, WHERE on the body, with what FORCE.
- The other person's RESPONSE: flinch, stiffen, try to remove his hands, create distance.
- DISGUISED resistance: she tried to remove his hands by turning it into "hand-holding" (a
  normal-looking couple gesture); he squeezed her hands and put his back on her hips. That
  disguised attempt is the whole point — "hands moved" erases it.

### Worked example — the test.mp4 interaction, told RIGHT (Jordan's own account is the bar)
> ~0:13 — John taps her butt, then places both hands on her hips. She tries to remove his hands
> by subtly turning it into hand-holding (something a normal couple does). He squeezes her hands,
> puts his hands back on her hips, and forcefully pulls her into his body. ~0:19 — his hands open
> but stay resting on her hips. ~0:22 — she actively moves away, creating distance.

What the earlier AI got wrong, concretely:
- Used "iliac crest / lateral thigh" jargon nobody understands.
- Said "hands moved / hands turned to an open position" instead of "he tapped her butt" and "he
  forcefully pulled her against him while she tried to stop him."
- Caught the obvious 0:22 move-away but **MISSED the 0:13 first resistance** (the disguised
  hand-removal + the forced pull-in). Missing the subtle-but-visible is the worst outcome.

## 4. Catch the subtle resistance/consent narrative — that IS the evidence.
Track the consent/resistance dynamic across the WHOLE interaction, not just the loud moment. The
quiet first attempt to stop it is often the most probative part.

## 5. CLEAR or it gets overlooked.
Unnatural phrasing buries a finding as surely as omitting it. Write so a busy detective
understands instantly on first read.

## 6. Known vs candidate → the tags
- `[DOCUMENTED]` = clearly visible / established → state it plainly, by name, in plain words.
- `[ANALYSIS]` / `[CANDIDATE]` / `[UNVERIFIED]` = inference → say it's an inference, show the basis.
Reserve hedging for genuine `[ANALYSIS]`. Do NOT hedge `[DOCUMENTED]` facts into mush.

## 7. For AGENTS and FUTURE TOOLS
Every agent calling these tools, and every new tool added, inherits this contract:
known → plain + named; uncertain → flagged with basis; dynamics over mechanics; plain words over
jargon; clarity over caution. When a model resists describing explicit-but-evidentiary contact,
the answer is the authorized-forensic framing **plus plain complete language** — never softening,
never Latin, never "hands moved." This is authorized investigative documentation, human-reviewed,
to protect a victim and to avoid falsely accusing the innocent. Describe it factually and fully.

---

# Visual comparison / frame-matching — an ITERATIVE workflow, not a one-shot tool
The hand-built `2601-Chatham-Cir-Occupancy-Logs.pdf` exhibit is the standard. Jordan (a
professional video editor, volunteering) made it by: cropping/rotating two shots to compensate
for different camera placement so the SAME room lined up and the match became visually obvious;
laying out one clear comparison per page with the critical info; and applying subtle, honest
color/brightness/contrast correction to an overexposed shot so a hidden vent became visible —
the kind of treatment a hired editor does for visual cohesion. No stretching, no AI diffusion,
no meaningful alteration.

**Goal:** make ANYONE instantly understand — SHOW what to look at, TELL where it's from (file,
timestamp, subject). One page = one clear comparison.

**It is iterative by nature** (adjust → look → adjust → look). The tool/workflow must support a
loop, not pretend to be one-shot:
1. **Surface** candidate same-location / same-subject frame pairs (becky-osint frames +
   perceptual hash → nearest pairs).
2. **Align** for the human eye: crop / rotate / scale to compensate for camera-placement
   differences so the same feature lines up and the match is obvious.
3. **Enhance — honestly only.** Brightness / contrast / color correction to reveal a feature an
   over- or under-exposed shot hides. The bar: only what a hired editor would do for cohesion.
   **FORBIDDEN:** stretching/warping geometry, AI diffusion/generation, cloning, or any change
   that alters content. NEVER fabricate. **Log every adjustment made** (a provenance line per
   image).
4. **Lay out** side-by-side with labels: source file, timestamp, subject, a one-line "what to
   look for," and the matching features pointed out.
5. **Review → adjust → re-render.** Expect several passes — build the loop in.

**Output:** a court-exhibit-grade page set a human confirms. Candidate-not-conclusion: it shows
the match for human verification + realtor/listing corroboration; it never declares "same place."

## Provenance / honesty (ALL visual output)
Work on COPIES — never modify source videos/images. Disclose every enhancement. The goal is to
REVEAL what is already there, never to create. An exhibit that can't survive "what did you do to
this image?" is worthless — so each page carries its own edit log.
