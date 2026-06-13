# Case Context — Review Prompt

This file is passed to `becky-review --case-context <path>`. Edit it per case.
It steers the LLM backend toward the entities, timeline, and priorities that
matter for THIS investigation. The mock backend ignores most of this (it has no
entity lookup) but the claude-code / openrouter backends use it directly.

## Known entities (names, aliases, nicknames, descriptions)

- **The Defendant** — primary subject of the recordings. Speaks in most segments.
  Aliases: (fill in).
- **The Wife** — the defendant's spouse, separated. Often referred to obliquely as
  "my ex", "she", "her", "the wife".
- (Add more named entities, their aliases, and one-line descriptions.)

## Timeline anchors

- Separation: ~Feb 2026 (so "my ex" after this date most likely = The Wife).
- Arrest date: (fill in).
- (Add dated anchors so the model can disambiguate temporal references.)

## Locations of interest

- The family home: (address / description).
- (Add locations; pair with descriptions so frames/cues can be matched.)

## What to flag

- Vague references that hide an identity or place ("my ex", "she", "the house").
- Admissions, denials of wrongdoing, threats, references to police/money/weapons.
- Identity / location cues that corroborate or contradict the record.

## What to ignore

- Ordinary small talk with no investigative value.
- Filler words and false starts.

## Investigation priorities

1. Resolve ambiguous references to the most likely named entity, with explicit
   reasoning and a calibrated confidence. State uncertainty honestly.
2. Surface notable moments that a detective would want to review.
3. Never invent facts; only use names justified by the entities/timeline above.
