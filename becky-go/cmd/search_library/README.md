# search_library

One dumb call, per `hj-mission-control/docs/library-contract.md`: Whoretana (or
any agent) asks a plain-English question, gets back scored hits from Jordan's
whole life-context library (bookmarks, history, GitHub stars, YouTube,
research, socials) merged with his AI chat transcripts.

```
search_library "<plain english query>" [--limit N] [--pretty]
```

- Default stdout: `{"ok":true,"results":[{"title","path","url","date","source","score","snippet"}]}`
- `--pretty`: high-contrast ANSI colored output for a human terminal.
- Exit 0 on success; nonzero with `{"ok":false,"error":"..."}` on failure.

## Backend

Shells out to `qmd search` (BM25, no LLM rerank) across two qmd collections:
`library` (`X:\AI-2\library`) and `transcripts` (Jordan's AI chats). qmd's
hybrid `qmd query` reranker was tested and took ~60s per call (LLM rerank on
GPU) — too slow for a live voice-assistant round trip, so this tool uses the
sub-second BM25-only `qmd search` path instead. `source` is `"ai-chats"` for
transcripts hits, otherwise the library subfolder the hit came from
(bookmarks, history, github-stars, youtube, instagram, tiktok, research, ...).

Ships without the `becky-` prefix (see the exception in `build-all-tools.bat`)
because the contract calls it by its literal name.

## Future: graphiphy (knowledge-graph search)

Not built here. When it exists, it would plug in as a third result source
merged alongside `library`/`transcripts` in `toResults` — same `Result`
shape, a new `source` value (e.g. `"graph"`), no change to the CLI contract.
