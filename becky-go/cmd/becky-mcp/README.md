# becky-mcp — let ANY agent use becky's tools (the standard way)

`becky-mcp` is an **MCP server**: it exposes every becky tool and workflow to an
MCP-capable agent (Claude Code, and others) as a **first-class, schema'd tool**. The
agent connects once and then *sees* becky's tools natively — no shelling raw commands,
no guessing arguments, no parsing. This is the fix for "my forensic agent tried to use
becky tools and it was a shit show": the shit-show was the agent having to drive raw
CLIs; MCP removes that entirely.

## Use it from Claude Code (your forensic agent)
Build the tools (`build-all-tools.bat` makes `becky-mcp.exe`), then add it once:

```
claude mcp add becky -- becky-mcp
```

or drop a `.mcp.json` in the agent's project folder:

```json
{ "mcpServers": { "becky": { "command": "becky-mcp" } } }
```

That's it. The agent now has tools like `becky-transcribe`, `becky-identify`,
`becky-ocr`, … and **`workflow:process-video`** (runs the whole transcribe → diarize →
ocr chain in one call). Tier (green/yellow/red) is shown in each tool's description.

## What it exposes
- **Every `becky-*` tool** from the shared `internal/catalog` (one source of truth — it
  can never drift from the real tools), each with an `input` (file path) schema.
- **Each workflow** from `internal/workflowdef` as `workflow:<name>` — one call runs the
  whole ordered, conditional chain.

## How it works
Newline-delimited JSON-RPC 2.0 over stdio (the MCP standard). Methods: `initialize`,
`tools/list`, `tools/call`. A `tools/call` shells the real `becky-*.exe` on this machine
and returns its output; a missing binary returns a clean `isError` message (never a
crash). Verified: unit tests + an end-to-end stdio run (initialize + tools/list return
19 tools incl. the workflow). The tools themselves run locally, where the binaries live.
