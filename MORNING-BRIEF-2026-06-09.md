# becky — what works now (2026-06-09)

**1. The test passes — becky figures out who it is, with NO hint from you.**
Run footage through `tools\ingest\ingest.bat`. The page now opens with, verbatim:
> **John Clancy** — the bearded man (voice + face). **Hair Jordan** — the green-haired man,
> addressed by name. Named because two signals agree, not guessed.
The pipeline reasons that the green-haired man called "Hair Jordan" IS Hair Jordan.
Verified on `boxing.mp4` tonight; the fact-check gate passed.

**2. becky-ask saves files now.** Drop a video on `becky-ask.exe`, press a number (or type
`transcribe, diarize`) → results land NEXT TO the video (`boxing.srt`, `boxing.diarize.json`).
Originals are never touched; a verified file is never overwritten.

**3. One ingest entry, not two.** Deleted the duplicate bat. `ingest.bat` writes next to the
file; pick "Into the wiki" in the popup (or `--wiki`) to send it there. No extra subfolders.

**4. Disclosures.** "this is Shelby" = a fact that guides the read. "Shelby told me…" = a lead
that does NOT bias the result until corroborated.

This isn't for court — it's to tell you where to look. It states what the data supports, plainly.
