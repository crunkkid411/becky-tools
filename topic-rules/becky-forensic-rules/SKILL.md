---
name: becky-forensic-rules
description: "Forensic footage rules for becky-tools: corroborate two independent signals before concluding who is on screen or speaking, recall is for detection never naming. Use for the criminal-case footage, face or voice identification, presence on screen."
---

# Forensic rules

Moved verbatim out of the always-loaded CLAUDE.md files on 2026-10-10 so it is read only when this kind
of work comes up. It has the same authority as CLAUDE.md.

- **Corroborate, then CONCLUDE — don't hedge.** ≥2 independent signals agreeing →
  state the conclusion plainly. A lone weak signal → "unknown"/candidate. A flood
  of maybes a human must sort = tool failure. The CONCRETE tool-chain for "is subject
  X actually on screen during [t0,t1]" is the **corroboration playbook in `SKILL.md`**
  (narrow with cheap signals → **`becky-validate` WATCHES the window with Gemma-4** →
  ≥2 agree → ship a TIGHT interval). A transcript mention or a `becky-motion` burst is
  NEVER presence; never put a window a model looked at — and the subject wasn't there —
  on a timeline anyway. (2026-06-24: a forensic task failed exactly here — the tools
  worked, the agent's chaining didn't.)

- **Recall is for DETECTION, not NAMING.** Surface every face/voice; attach a NAME
  only when corroborated.

- **Never a last name alone - use the full name.** There is nobody called "Clancy": John Anthony
  Clancy, Shelby Alyse Clancy, Jake Clancy and Bettina Burke-Clancy are four people. Jordan,
  2026-10-11: "do not use last names to identify someone when their full name is available, far too
  problematic." Write "John Clancy" / "Shelby Clancy" (or the full KB name), in reports, docs,
  code comments and prompts alike. Shelby Alyse Clancy = Shelby Clancy = Shelby Kerberg = Shelby
  Alyse Kerberg = PopTart Barbie (one person; KB aliases). Enforced in code: `becky profile`
  (cmd/becky `tokenSubset`) needs the first name, becky-revsearch matches whole names only, and the
  who-is-this window lists everyone sharing a typed last name instead of picking one.

- **Due diligence before asking Jordan, then ask on his screen - never in a file.** A tool asks
  Jordan "who is this?" only after every check it has ran (becky-identify: frame-by-frame, Gemma
  face + whole person, Falcon, reverse image search, voice), and only through `internal/whois`
  (the one popup window). Never put a question in a markdown/JSON file for him to find.
