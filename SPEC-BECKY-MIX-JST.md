# SPEC-BECKY-MIX-JST.md — the Joey Sturgis mix, encoded as a deterministic routine

> **STATUS: design only — NOT built (2026-06-15).** A knowledge+build spec that
> encodes the Joey Sturgis / JST modern-metalcore mix as a **deterministic mix
> graph** that layers onto `becky-compose`'s `project.json` and renders through
> `becky-canvas`'s mixer. The headline ask: when a profile has a `breakdown`
> section, becky **automatically** sets up the kick/drum → bass/guitar sidechain
> ducking so the low end stays defined and the breakdown breathes — declared as a
> handful of `{from,to,kind:"sidechain"}` edges, not 100 manual moves. Authored
> from a 15+yr pro producer's request; awaits Jordan's go/no-go before scaffolding.

---

## 1. Intent (read carefully)

`becky-compose` already emits the deterministic audio DAG (tracks→buses, an
isolated 808/bass bus, `routing` sidechain edges, per-bus `fx:[]`). What it does
**not** yet do is carry a *mix* — the ordered FX chain per bus and the breakdown
ducking that make a metalcore mix sound like a record instead of a stack of stems.

This spec is the **knowledge layer**: Joey Sturgis's production pillars and the
breakdown sidechain routine, frozen as numeric defaults the way `becky-compose`
freezes a genre as numeric music theory. A mix becomes a **declared graph, not a
diary of manual moves** — which is exactly becky's ethos: deterministic,
repeatable, "≥2 signals → conclude". Two independent signals here are *the
profile has a `breakdown` section* **and** *an isolated low-end source (808/bass)
exists* → becky concludes "duck the lows to the kick" and emits the edges. No
hedging, no 100-click hunt through a DAW's bus matrix.

Joey Sturgis is the producer/mixer behind the modern metalcore sound (Asking
Alexandria, The Devil Wears Prada, Of Mice & Men, We Came As Romans, Attack
Attack!, I See Stars) and the founder of **Joey Sturgis Tones (JST)**. becky's
built-in FX nodes are the **deterministic floor**; the matching JST plugin is
named as the **optional VST3/CLAP equivalent** per the selective-hosting policy in
`SPEC-BECKY-CANVAS.md`.

## 2. The "Joey Sturgis sound" — production pillars (the knowledge)

Each pillar is a real, cited technique, reduced to something becky can declare.

1. **Drum sample replacement / triggering.** Tight, consistent one-shot samples
   blended with (or replacing) the recorded kit so every kick/snare hits the same
   — Sturgis's own `Drumshotz` library and `Drumagog`/`DF-SMACK`-style triggering
   are the canonical workflow. becky's deterministic floor models this as a
   **trigger→sample node** on `kick`/`snare` with a blend amount; "DF-SMACK on
   everything, cranked" becomes a default saturation+transient stage on the drum
   bus. ([CreativeLive — Drum Sample Replacement with Drumagog](https://www.creativelive.com/class/studio-pass-joey-sturgis/lessons/drum-sample-replacement-with-drumagog),
   [JST — How To Sample Replace Drums](https://joeysturgistones.com/blogs/latest-videos/how-to-sample-replace-drums),
   [Drumforge — Drumshotz Joey Sturgis](https://drumforge.com/products/drumshotz-joey-sturgis))
2. **Aggressive gain staging + low-end build-up control.** Sturgis: *"I had to
   build a system that could handle low-end buildup for really fast kick parts"* —
   a multiband compressor with relaxed settings that only engages during intense
   kick passages. becky models this as a per-bus **gain** node early in the chain
   plus a **multiband/sidechain comp** gated to kick energy. ([CreativeLive —
   Mixing Master Class](https://www.creativelive.com/classes/mixing-master-class-joey-sturgis))
3. **Gating.** Tight noise gates on rhythm guitars and kick/snare to kill bleed
   and chug-tail mud → a **gate** node with fast attack, program-dependent
   release. ([Nail The Mix — Metal Mixing 101](https://www.nailthemix.com/metal-mixing-101-buster-odeholms-eq-compression-fx-tips))
4. **Parallel / bus compression.** Drums get a smashed parallel bus blended under
   the dry kit for thickness and punch; the master/2-bus gets glue. ([Nail The Mix
   — Mixing Metal Drums: Parallel Compression](https://www.nailthemix.com/mixing-metal-drums-parallel-compression))
5. **Scooped-then-present metalcore guitar.** High-gain rhythm tone, low-mid scoop
   to clear room for the kick/snare, presence/upper-mid push so the riff cuts;
   HPF to clear cabinet thump; IRs (Sturgis's `Conquer All` packs) for the cab.
   ([Riffhard — How to Mix Metalcore](https://www.riffhard.com/how-to-mix-metalcore/),
   [JST — Conquer All](https://joeysturgistones.com/products/conquer-all))
6. **Tight low-end discipline.** Kick, bass, and down-tuned guitar all fight for
   the same lows; the fix is strict HPF on guitars, a dedicated sub layer on bass
   (sine/808), and **sidechain ducking** so only one source owns the sub at a
   time. ([Mastering The Mix — Punchy Low End](https://www.masteringthemix.com/blogs/learn/creating-a-punchy-low-end-tips-for-balancing-kick-and-bass),
   [Streaky — Bass and Kick Sidechain Techniques](https://www.streaky.com/blogs/blog/bass-and-kick-sidechain-techniques-two-ways-to-tighten-your-low-end))

## 3. The breakdown sidechain routine (the headline ask)

When a profile section is a **breakdown**, the lows are the whole point and the
biggest risk: palm-muted chugs, a sub/808, and a down-tuned rhythm guitar all pile
onto the same 30–120 Hz band and turn to mud. The routine keeps the kick's
transient and sub punching through by **ducking everything that competes with it
out of the way for a split second on each hit** — "an ultra-fast, almost invisible
ducking effect" that carves a pocket for the kick.
([Nail The Mix — Sidechain Compression for Punchy Metal Mixes](https://www.nailthemix.com/sidechain),
[Audient — Beginner's Guide to Sidechaining](https://audient.com/tutorial/the-beginners-guide-to-sidechaining/))

### 3.1 Who ducks whom (the declared edges)

becky-compose already declares `kick→music-bus` and `kick→808`. The JST breakdown
routine **extends** that into a complete low-end pocket. Sources are detectors
(control taps); destinations are the `.sidechain` input of a compressor on that
bus. Edges becky emits for any section flagged `breakdown` (skip any whose
endpoints don't exist):

```jsonc
"routing": [
  { "from": "kick", "to": "bus.808.sidechainComp",     "kind": "sidechain",
    "note": "808/sub ducks hard to the kick so only one source owns the sub" },
  { "from": "kick", "to": "bus.bass.sidechainComp",     "kind": "sidechain",
    "note": "DI bass ducks to the kick; keeps the kick transient clean" },
  { "from": "kick", "to": "bus.gtrRhythm.sidechainComp","kind": "sidechain",
    "note": "down-tuned rhythm chug ducks LOWS only to the kick (band-split)" },
  { "from": "snare","to": "bus.808.sidechainComp",      "kind": "sidechain",
    "amount": 0.5,
    "note": "optional: 808 also nods to the snare on breakdown backbeats" }
]
```

Two corroborating signals → conclude. If the profile has **no** isolated low-end
source (no 808/bass bus), becky emits **nothing** rather than guessing — a lone
weak signal stays "unknown", consistent with becky's anti-hedge rule.

### 3.2 Attack / release feel and amount (the deterministic defaults)

The feel is "fast and invisible". Defaults, by destination, encoded so the engine
is byte-stable:

| Source → destination        | Reduction | Ratio | Attack | Release       | Band            |
|-----------------------------|-----------|-------|--------|---------------|-----------------|
| kick → **808/sub**          | 6 dB      | 6:1   | 0.5 ms | 80 ms (or to tempo) | full / <150 Hz |
| kick → **DI bass**          | 4 dB      | 4:1   | 1 ms   | 60 ms         | full            |
| kick → **rhythm gtr (lows)**| 3 dB      | 4:1   | 1 ms   | 50 ms         | **band-split <120 Hz only** |
| snare → 808 (optional)      | 3 dB      | 4:1   | 2 ms   | 40 ms         | full            |

Starting point per the cited material: **3–6 dB of reduction, ratio ~4:1, attack
FAST.** ([Nail The Mix — Sidechain](https://www.nailthemix.com/sidechain),
[Streaky](https://www.streaky.com/blogs/blog/bass-and-kick-sidechain-techniques-two-ways-to-tighten-your-low-end))
The rhythm-guitar duck is **frequency-selective**: only the lowest band ducks so
the riff's body and pick attack ring out while the kick punches through the sub —
"frequency-selective ducking" / dynamic-EQ ducking.
([Streaky](https://www.streaky.com/blogs/blog/bass-and-kick-sidechain-techniques-two-ways-to-tighten-your-low-end))
**Release** defaults to a fixed ms value but may resolve to a tempo division
(e.g. 1/16) when `bpm` is known, keeping the duck musical and still deterministic.

### 3.3 Where it lives

These edges and the `sidechainComp` node settings are emitted **only** for
sections with `kind:"breakdown"` (or a `breakdown` arrangement entry per
`SPEC-BECKY-COMPOSE.md` §6). Outside the breakdown, the chain still carries a
gentle always-on kick→bass duck (the metalcore default), but the **aggressive**
guitar-low duck is breakdown-scoped so verses/choruses keep their width.

## 4. Deterministic mix-chain template per bus

Every chain below is an **ordered list of becky built-in FX nodes** (the
deterministic floor) with default parameters, and the matching JST plugin named as
the **optional** VST3/CLAP equivalent (selective hosting only — built-in floor
always works offline). becky built-ins available:
`gain · eq · gate · compressor(+sidechain) · saturation · delay · reverb · limiter
· trigger/sample · bandsplit`. Order is fixed → topo-sortable → reproducible.

### 4.1 Kick
`gate → trigger/sample (blend) → eq → compressor → saturation`
- HPF ~40 Hz; tighten 200–400 Hz mud; presence "click" boost 2.5–4 kHz; sub bump
  ~60–82 Hz. Gate fast. Comp 4:1, ~10 ms attack, fast release.
  ([Develop Device — Drum Mixing Cheat Sheet](https://developdevice.com/blogs/news/the-ultimate-drum-mixing-cheat-sheet-a-complete-guide-to-a-perfect-sound))
- **JST equiv:** Drumforge `DF-SMACK` / `Drumshotz`; `Sub Destroyer` for sub
  consistency. ([JST — Sub Destroyer](https://joeysturgistones.com/products/sub-destroyer))

### 4.2 Snare
`gate → trigger/sample (blend) → eq → compressor → saturation`
- Body ~150–250 Hz; cut boxy 400–600 Hz; crack 3–6 kHz; gate to kill bleed; comp
  4:1 medium attack to keep transient.
- **JST equiv:** `Drumshotz` / `DF-SMACK`.

### 4.3 Drums bus
`eq → compressor (glue) → [parallel: compressor (smash) ] → saturation → limiter`
- Bus glue comp ~4:1, **20 ms attack** (let transient through), **10 ms release**;
  blend a parallel smashed copy under the dry kit; gentle bus saturation.
  ([Mastering.com — Punchy Drums](https://mastering.com/mixing-punchy-drums/),
  [Nail The Mix — Parallel Compression](https://www.nailthemix.com/mixing-metal-drums-parallel-compression))
- **JST equiv:** `Gain Reduction` (bus comp), `Finality` (bus limiter).

### 4.4 808 / bass bus (the isolated low-end bus from compose)
`eq → bandsplit → sidechainComp (kick) → saturation → limiter`
- Sub layer (sine/808) kept mono and tight; **kick→808 sidechain** is the always-
  on default, deepened in breakdowns (§3); HPF the air off the sub; saturate for
  audibility on small speakers.
  ([Mastering The Mix — Punchy Low End](https://www.masteringthemix.com/blogs/learn/creating-a-punchy-low-end-tips-for-balancing-kick-and-bass))
- **JST equiv:** `Sub Destroyer`, `Bassforge`, `Finality` (limiter).

### 4.5 Rhythm guitar bus
`hpf/eq (scoop) → gate → bandsplit → sidechainComp (kick, lows only) → compressor → saturation/IR`
- **HPF 80–120 Hz** (tuning-dependent; 65–105 Hz for very low tunings) to clear
  cab thump; **low-mid scoop** ~400–600 Hz; **presence push** 2–4 kHz so the riff
  cuts; tight gate; band-split kick duck on the lows only (§3.2).
  ([Riffhard](https://www.riffhard.com/how-to-mix-metalcore/),
  [Nail The Mix — Metalcore guitar tones](https://www.nailthemix.com/bilmuri-metalcore-subs-synths-sidechains))
- **JST equiv:** `Toneforge` (amp/IR), `Conquer All` IRs; the user's **"The Odin
  II"** registered as his lead/rhythm VST (see §6). ([JST — Toneforge](https://joeysturgistones.com/collections/toneforge))

### 4.6 Lead guitar
`hpf/eq → compressor → saturation → delay → reverb`
- Brighter, more present than rhythm; light comp for sustain; tasteful slap delay
  + plate for size; sits above the scoop.
- **JST equiv:** `Toneforge` (e.g. Jeff Loomis / Disruptor); user VST per §6.

### 4.7 Synth / pad
`eq → sidechainComp (kick, optional) → reverb`
- Atmospheric synths/pads can duck to the kick to clear the transient — "sidechain
  atmospheric guitars or synth pads to the kick" with release dialed to the
  track. ([Nail The Mix — Sidechain](https://www.nailthemix.com/sidechain))

### 4.8 Vocal bus
`eq → gate → compressor → saturation → delay → reverb → limiter`
- Sturgis's signature consistent, "mix-ready" vocal: HPF, de-mud, hard comp
  (parallel blend), presence/air, tight verbed/delayed throws.
- **JST equiv:** `Gain Reduction Deluxe` (the canonical Sturgis vocal-comp, with
  parallel blend), `Finality` (limiter). ([JST — Gain Reduction Deluxe](https://joeysturgistones.com/products/gain-reduction-deluxe),
  [Everything Recording — Finality review](https://everythingrecording.com/review-joey-sturgis-tones-finality-limiter/))

### 4.9 2-bus / master
`eq → compressor (glue) → limiter (Finality)` — gentle glue, transparent ceiling.

> All node parameters live in JSON as named constants — no magic numbers buried in
> Go. The above are **defaults**; the mix-preferences layer (§6) overrides them.

## 5. How it plugs in — `mix.json` over `project.json`

### 5.1 The option

`becky-compose --genre metalcore --mix jst` (and a bare `becky-mix
project.json --profile jst` for an existing project) **layers a mix onto** the
routing manifest — it does not regenerate MIDI. The mix is a separate
content-addressed artifact so a stem stays a stem and a mix stays reproducible.

### 5.2 The artifact

`mix.json` (sibling of `project.json`) declares, per bus, the **ordered FX chain**
from §4 and the **breakdown sidechain edges** from §3:

```jsonc
{
  "schemaVersion": 1,
  "profile": "jst",
  "deterministic": true,
  "appliesTo": "project.json",          // content hash of the source project
  "busFx": {
    "bus.gtrRhythm": [
      { "type": "eq",         "id": "gtrRhythm.hpf",   "params": { "hpfHz": 90, "scoopHz": 500, "scoopDb": -3, "presenceHz": 3000, "presenceDb": 3 } },
      { "type": "gate",       "id": "gtrRhythm.gate",  "params": { "attackMs": 0.5, "releaseMs": 80 } },
      { "type": "bandsplit",  "id": "gtrRhythm.split", "params": { "crossoverHz": 120 } },
      { "type": "compressor", "id": "gtrRhythm.scLow", "params": { "ratio": 4, "attackMs": 1, "releaseMs": 50, "band": "low", "sidechain": true } }
    ]
  },
  "breakdownRouting": [ /* the §3.1 edges, emitted only when a breakdown section exists */ ],
  "vstMap": { /* §6 user overrides */ }
}
```

- **Layering, not mutation** (becky immutability rule): `mix.json` references
  `project.json` by content hash and never edits it; re-running with the same
  `(project, profile, mix-prefs)` yields a **byte-identical** `mix.json`.
- **becky-canvas renders it:** the mixer in `SPEC-BECKY-CANVAS.md` already turns
  the routing DAG into nodes/edges; `mix.json` simply supplies each bus's `fx[]`
  chain and the extra sidechain edges. The "one declaration = one sidechain" promise
  (Canvas §5) is exactly how §3's edges are realized — the engine auto-creates the
  send, detector tap, and band-split, named deterministically.
- **Built-in floor is always offline.** VST3/CLAP equivalents (§4) load only when a
  trusted plugin in `vstMap` is present; absent → degrade to the built-in node,
  never crash (becky degrade-never-crash invariant).

### 5.3 The pipeline

```
becky-compose --genre X            → project.json (+ stems)        [BUILT]
becky-mix project.json --profile jst → mix.json                    [THIS SPEC]
becky-canvas project.json mix.json → live mixer with the JST chain  [SPEC-BECKY-CANVAS]
```

A JST `profiles/jst.mix.json` template ships embedded (`go:embed`, like the genre
DB) so "apply the Joey Sturgis mix" is a cached default, not a research step.

## 6. Mix preferences — register your own plugins/presets per bus

The producer will name his own trusted VSTs/presets over time. A small
**mix-preferences** file lets him do that without touching the engine — the same
"research once, cache forever" pattern as the genre DB:

```jsonc
// becky-go/internal/music/mixprefs/<user>.json   (or ~/.becky/mixprefs.json)
{
  "schemaVersion": 1,
  "busPreferences": {
    "bus.gtrLead":   { "vst": "The Odin II", "preset": "", "fallbackToBuiltin": true },
    "bus.gtrRhythm": { "vst": "The Odin II", "preset": "" }
  },
  "paramOverrides": {
    "bus.gtrRhythm.scLow": { "releaseMs": 40 }
  }
}
```

- **First registered entry: "The Odin II"** as the user's current guitar/lead VST
  (rhythm + lead). More VSTs/presets to be added tomorrow per the user.
- Preferences **override** the §4 defaults at layer time; missing/untrusted VSTs
  fall back to the built-in node (`fallbackToBuiltin`, default true). Resolution
  order: **user mix-prefs → profile (jst) defaults → built-in floor.**
- Stays deterministic + content-addressed: prefs are part of the hash that makes
  `mix.json` reproducible.

## 7. Build split (cloud vs local)

| Cloud / web agent                                   | Local agent (Jordan's Win10 PC)                 |
|-----------------------------------------------------|-------------------------------------------------|
| `mix.json` schema + the embedded `jst.mix.json`     | Wire real VST3/CLAP hosts (Toneforge, GR Deluxe)|
| Pure-Go `becky-mix` layerer (project→mix, hashing)  | A/B the built-in floor vs the JST plugins by ear|
| §3/§4 defaults as JSON constants + unit tests       | Tune amounts/release-to-tempo on real stems     |
| Deterministic/byte-identical tests; breakdown-edge  | Verify the breakdown "breathes" on real footage |
| emission logic; degrade-to-floor logic              | of a mix; confirm sidechain feel                |

**Helper/host stub contract:** the VST/CLAP host is a documented stub with a fixed
input/output contract (`load(plugin, preset) → node`; `process(buffer) → buffer`);
the cloud agent leaves it stubbed and the local agent plugs in the real host. The
built-in FX floor is fully implementable and testable on the cloud side with no
models/plugins. CI stays green on Linux+Windows (no plugins, no audio device).

## 8. Open decisions for Jordan

1. **Tool shape:** a flag on compose (`becky-compose --mix jst`) **and** a
   standalone `becky-mix` over any `project.json`, or only one of those?
2. **Breakdown release:** fixed-ms ducking (simplest, always deterministic) or
   resolve release to a tempo division (1/16, 1/8) when bpm is known (more musical,
   still deterministic)? Default proposed: tempo-resolved with ms fallback.
3. **Rhythm-guitar low duck:** band-split compressor (built-in) vs dynamic-EQ
   ducking for the lows — which is the becky default floor?
4. **Drum replacement scope:** ship a tiny default one-shot set with becky for the
   `trigger/sample` node, or leave samples user-supplied and built-in floor =
   transient+saturation only (no bundled audio)?
5. **JST plugin map:** treat the named JST plugins (Toneforge, Gain Reduction
   Deluxe, Finality, Sub Destroyer, Drumshotz/DF-SMACK, Conquer All IRs) purely as
   *documentation* of the optional equivalent, or actually pin a small trusted set
   for the selective host once VST3/CLAP lands?
6. **Mix-prefs location:** per-user file in the repo (`mixprefs/<user>.json`,
   versioned) vs a home-dir dotfile (`~/.becky/mixprefs.json`, private)? "The Odin
   II" is the first entry either way.
7. **Network posture:** mix layering is fully offline/deterministic; the only
   online surface is *researching a new mix profile* (like adding a genre) — confirm
   offline-default, opt-in + logged, consistent with the other becky specs.

---

> **Sources** (mix knowledge, cited inline above): JST/CreativeLive Sturgis master
> classes and tutorials; Nail The Mix sidechain/parallel/metalcore-guitar articles;
> Streaky and Mastering The Mix low-end/kick-vs-bass; Riffhard metalcore mixing;
> Develop Device / Mastering.com drum settings; JST product pages (Gain Reduction
> Deluxe, Finality, Sub Destroyer, Toneforge, Conquer All) and Drumforge Drumshotz.
> Plugs into `SPEC-BECKY-COMPOSE.md` (the `project.json` it layers on) and
> `SPEC-BECKY-CANVAS.md` / the planned DAW engine (the mixer that renders it).
