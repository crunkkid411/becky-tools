# SPEC-RADAR.md — `becky-radar`: turn "things Jordan flagged" into becky action

> **STATUS: design only — NOT built (2026-06-15).** Written after a real miss:
> Jordan opened **PP-OCRv6** (a new OCR model) in **Chrome on his iPhone** as the
> canonical example of a tool that should watch his browsing and surface updates.
> That tool did not exist, so becky-ocr stayed on PP-OCRv5. This spec is the fix.
> Pairs with the shipped `becky-freshness` (which watches becky's *pinned*
> dependencies); `becky-radar` watches what *Jordan* flags and joins the two.

---

## 1. The problem (first principles)

Jordan does his discovery on his phone. He **deliberately uses Chrome on iOS, not
Safari**, specifically so the pages he opens (HuggingFace model cards, GitHub
repos, arXiv papers, articles) land in his **Chrome history** — his de-facto
"look at this later" queue. Nothing in becky reads that queue, so:

- A flagged improvement (PP-OCRv6) is invisible to becky until Jordan manually
  re-types it from memory — exactly the failure that prompted this spec.
- The only "memory" of Jordan's intent is Jordan. That is the load-bearing bug.

The cure is a tool that **ingests Jordan's browsing, recognises the high-signal
items, and cross-references them against becky** — so "I looked at PP-OCRv6"
automatically becomes "becky-ocr pins v5; here's the gap."

## 2. The key realisation — no iPhone access needed

iPhone Chrome history does **not** have to be read off the phone. Chrome on iOS,
signed into a Google account with Sync on, **syncs history to that account**, and
that synced history lands in the **local desktop Chrome History database** on the
same account. So the tractable, fully-offline source is already on Jordan's PC:

```
%LOCALAPPDATA%\Google\Chrome\User Data\<Profile>\History    (SQLite)
```

The `urls` + `visits` tables carry every visited URL with title and timestamps,
**including foreign (synced-from-iPhone) visits** — recent Chrome tags synced
visits with an originator device GUID, so iPhone-origin rows are even
distinguishable. No iCloud, no Google OAuth, no iTunes backup, no network.

**Sources, in priority order (each a `--source`):**
1. `chrome-local` (default) — the desktop Chrome History DB above (carries synced
   iPhone history). The 80% path.
2. `itunes-backup` — parse an unencrypted iTunes/Finder backup at
   `%APPDATA%\Apple Computer\MobileSync\Backup\<id>` (Manifest.db -> the
   `com.google.chrome.ios` History file) for users who don't desktop-sync. Phase 2.
3. `urls-file` — a plain text/JSON list Jordan pastes, as a manual escape hatch.

> **Trap (must handle):** Chrome keeps the History DB **locked** while running.
> Copy it to a temp file first (`History` + `History-wal`) and open the copy
> read-only — never touch the live DB. (Same pattern forensic tools use.)

## 3. What it does (pipeline)

```
becky-radar [--source chrome-local] [--since 14d] [--db forensic.db]
            [--enrich] [--output radar.json]

1. LOCATE + COPY the history source read-only (never lock the live DB).
2. EXTRACT visits since --since: {url, title, visit_time, device, visit_count}.
3. CLASSIFY each into a signal class with cheap deterministic rules:
     - hf-model      huggingface.co/<org>/<repo>            (the becky case)
     - github-repo   github.com/<org>/<repo>
     - arxiv         arxiv.org/abs/NNNN
     - model-keyword title/url contains ocr|asr|embedding|diariz|vlm|llama|...
     - article       everything else high-signal (dedup low-value domains)
4. CROSS-REFERENCE becky:
     - map each hf-model / github-repo to the becky-freshness manifest. A hit
       (e.g. PaddlePaddle/PaddleOCR-VL or .../PaddleOCR) => attach "becky-ocr
       pins <pinned>; you viewed <url>" with the gap.
     - unknown but model-shaped items => "candidate new tool/model — review".
5. SYNTHESISE a plain-language report ranked by actionability:
     "You looked at PP-OCRv6 (PaddleOCR). becky-ocr uses v5 -> upgrade candidate."
6. (optional) --db writes a `radar` table so the corpus is queryable;
   --enrich fetches the HF/GitHub page once (the only online step, logged).
```

## 4. Output contract (synthetic values)

```json
{
  "tool": "becky-radar v1.0.0",
  "source": "chrome-local",
  "since": "2026-06-01T00:00:00Z",
  "items": [
    {
      "url": "https://huggingface.co/PaddlePaddle/PP-OCRv6_medium_rec_onnx",
      "title": "PP-OCRv6 medium rec (ONNX)",
      "first_seen": "2026-06-10T22:14:00Z",
      "device": "iPhone (synced)",
      "class": "hf-model",
      "becky_match": {
        "dependency": "paddleocr-pipeline",
        "used_by": ["becky-ocr"],
        "becky_pinned": "PP-OCRv5",
        "verdict": "UPGRADE CANDIDATE — you viewed a newer OCR version than becky uses"
      }
    }
  ],
  "candidates_new": [
    {"url": "https://github.com/foo/new-thing", "class": "github-repo",
     "verdict": "unknown to becky — possible new tool, review"}
  ],
  "notes": {}
}
```

Same FORENSIC-OUTPUT-PHILOSOPHY discipline as the rest of becky: a viewed URL is
one signal -> **candidate**; a viewed URL that maps to a tracked becky dependency
with a version gap is **corroborated** -> stated plainly as an upgrade candidate.
It never auto-changes anything; it surfaces, ranks, and concludes.

## 5. Build split (cloud <-> local)

| Cloud / web agent                                   | Local agent (Jordan's PC)                     |
|-----------------------------------------------------|-----------------------------------------------|
| This spec; URL-classifier rules; the `radar` schema | Real Chrome DB path + profile detection       |
| Go orchestrator (`cmd/radar/`), JSON contract       | Run against the live (copied) History DB      |
| Unit tests (classifier, freshness cross-ref) on     | iTunes-backup parser (Phase 2) on a real      |
| synthetic rows + a fixture SQLite                   | backup; tune the "high-signal" domain rules   |
| Cross-reference into `internal/freshness` manifest  | Confirm synced iPhone rows actually appear    |

Pure-Go + `modernc.org/sqlite` (already used by `beckydb`) reads the copied
History DB with **no cgo, no Chrome, no network** — fits becky's offline core; the
only online step is opt-in `--enrich`.

## 6. Why this is the systemic fix (with becky-freshness)

- `becky-freshness` (shipped) answers **"is what becky pins stale upstream?"**
- `becky-radar` (this) answers **"what did Jordan flag that becky should act on?"**
- Together they close the loop that let PP-OCRv6 slip: one watches becky's deps,
  the other watches Jordan's attention, and both cross-reference the same
  manifest. Run them as standard practice (e.g. the "Get Becky Updates" button can
  end by running both and printing a one-screen "radar + freshness" digest).

## 7. Open decisions for Jordan
1. **Default source:** confirm `chrome-local` (read the desktop Chrome DB that
   carries synced iPhone history) as the v1 path — is Chrome on this PC signed
   into the same Google account as the iPhone? (If not, Phase-2 iTunes-backup
   parse becomes primary.)
2. **Profile:** which Chrome profile (Default / Profile 1)? Auto-detect the most
   recently used unless told.
3. **Privacy posture:** radar reads ALL browsing. Restrict to high-signal domains
   (HF/GitHub/arXiv) only, or ingest everything and classify? Default: classify
   everything locally, surface only high-signal (nothing leaves the machine).
4. **Cadence:** on-demand only, or also run inside the update button as a digest?
