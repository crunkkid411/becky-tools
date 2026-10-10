# Routing skills and tools so unused ones cost nothing

Date: 2026-10-09. Read-only research. I changed nothing under ~/.claude or in any project, ran no hooks, and started no MCP servers.

## Bottom line

Skills are not the biggest cost on this machine. Instruction files loaded at every launch are bigger, and Claude Code already caps how much skill description text it shows. The design that saves real tokens has three parts:

1. Keep large text out of always-loaded files.
2. Hide rarely used skills from the list. They stay runnable with /name.
3. Use a deterministic UserPromptSubmit hook that injects a skill's body only when the prompt matches it.

A router on its own saves nothing, because it only adds text when it fires. System One fits as an optional second opinion on unclear prompts, inside the $5/month cap, never as the main path. Nothing has been changed. Every step touches Jordan's own config, so each one needs his yes.

## 1. What loads at startup on this machine (measured)

Method: read-only Python 3.14 scripts in the scratchpad (measure_skills.py, measure_unique.py; outputs measure.json and measure_unique.json). Symlinks were followed. Tokens are characters divided by 4, a rough estimate. Folder names stand in for skill names. One script had a key-name bug; I fixed it and re-ran it.

### 1a. Instruction files (loaded at launch; session folder X:\AI-2\becky-tools)

| File | Lines | Bytes | How it loads |
|---|---|---|---|
| C:\Users\only1\.claude\CLAUDE.md | 73 | 4,297 | At launch [S6] |
| Its @import: skills\agent-firefox\SKILL.md | 87 | 5,355 | At launch, full body via @import [S6] |
| Its @import: memory\MEMORY.md | 9 | 1,682 | At launch [S6] |
| C:\Users\only1\.claude\rules\common\*.md (10 files) | 10 files | 17,277 | At launch; none has a paths: field, so none is path-scoped [S6] |
| Auto memory: projects\X--AI-2-becky-tools\memory\MEMORY.md | 93 | 13,289 | At launch; first 200 lines or 25 KB [S6] |
| X:\AI-2\becky-tools\CLAUDE.md | 434 | 34,173 | At launch [S6] |
| X:\AI-2\CLAUDE.md (parent folder) | 218 | 22,506 | At launch; files in parent folders load too [S6] |
| Total (16 files) | | 98,579 | About 24,600 tokens |

### 1b. Skills

- User skills: 169 unique SKILL.md files, none hidden. Descriptions total 51,086 characters (about 12,800 tokens). All have descriptions.
- Enabled plugin skills: 943 files. 938 are listed; 5 are hidden by disable-model-invocation. 6 have no description. Descriptions total 204,285 characters after the 1,536-character per-entry cap (about 51,100 tokens).
- Together, skill description text is about 255,000 characters (about 63,800 tokens) before any budget is applied.
- Skill names: about 16,700 characters (about 4,200 tokens). Names are always listed [S1].
- Overlap: 61 user skill folder names also appear among enabled plugin skill folders. I did not compare contents, so I cannot say they are duplicates.

Budget arithmetic [S1]. The listing budget is 1% of the context window. I do not know this session's context window.

| Window | 1% budget, tokens | 1% budget, characters (divided by 4) |
|---|---|---|
| 200,000 | 2,000 | about 8,000 |
| 400,000 | 4,000 | about 16,000 |
| 1,000,000 | 10,000 | about 40,000 |

255,000 characters of descriptions is far above every row. So the least-used skills almost certainly have their descriptions dropped. This matches my own skill list, where many entries show only a name. That observation is not measured.

### 1c. Agents, commands, plugins, MCP servers, hooks

- Enabled plugin agent files: 144 files, 26,534 characters. Enabled plugin command files: 206 files, 15,330 characters. Together about 10,500 tokens. The plugin docs say the name and description of every agent and command Claude can invoke are in context on every turn [S5]. I found no cap for these in the docs I read.
- User commands: 97 files (3 without a description), 6,101 characters (about 1,500 tokens).
- User agent folder: 266 markdown files, of which 72 have a description. This is not a clean agent count. It totals 9,496 characters (about 2,400 tokens) as an upper bound.
- enabledPlugins: 130 keys. 129 are true and 1 is false (superjawn@claude-skills-journalism, installed but disabled). stagehand@claude-plugins-official is enabled but not installed. So 128 plugins are enabled and installed.
- Largest enabled plugin: everything-claude-code (1 plugin), with 459 skills, 48 agents, 79 commands and 6 MCP servers. Its skill, agent and command text is 85,920 characters, which is 34.9% of all enabled-plugin text (246,149 characters).
- Other large marketplaces: claude-plugins-official (33 plugins, 128 skills, 13 MCP servers); knowledge-work-plugins (8 plugins, 89 skills, 31 MCP servers); ruflo (32 plugins, 79 skills, 43 agents, 38 commands).
- MCP servers declared by enabled plugins: 62. Their tool names and server instructions enter context [S7]. Not measured.
- Hooks in settings: 76 entries across 11 events. 7 run on every prompt (UserPromptSubmit) and 16 run at session start. I did not run them, so the text they inject is not measured.

### 1d. Standing total (rough)

| Source | Tokens (approx.) |
|---|---|
| Instruction files | 24,600 |
| Skill names | 4,200 |
| Skill descriptions after the 1% budget | 2,000 to 10,000 |
| Plugin agent and command descriptions | 10,500 |
| User commands | 1,500 |
| User agent files (upper bound) | 2,400 |
| Total | about 45,000 to 53,000 |

Not in the total: MCP tool names (several hundred names appear in this session's deferred tool list; I did not count them exactly), MCP server instructions, hook output, built-in tool definitions, and the system prompt. Only /context in a live session gives the true figure.

## 2. How Claude Code loads and hides things (official docs)

- Skill names are always listed. Descriptions are cut to fit the 1% budget, and the least-invoked skills lose their descriptions first. Each entry's text is capped at 1,536 characters [S1].
- Knobs: skillListingBudgetFraction, example 0.02 [S3]; the environment variable SLASH_COMMAND_TOOL_CHAR_BUDGET, a fixed character count [S1]; skillListingMaxDescChars [S3]; skillOverrides "name-only" [S2].
- /doctor estimates the listing's cost. /context's Skills row shows the listing after the budget, which is what the model receives. /skill-doctor finds unused skills [S1].
- A skill's full body loads only when it is invoked, and then stays in the conversation. After compaction, only its opening is kept [S1].
- disable-model-invocation: true removes the skill from Claude's context entirely. The skill can still be run by name [S1].
- skillOverrides is set in settings, not in SKILL.md [S2]:

| Value | Listed to Claude | In the / menu |
|---|---|---|
| "on" (default) | name and description | yes |
| "name-only" | name only | yes |
| "user-invocable-only" | hidden | yes |
| "off" | hidden | hidden |

  Plugin skills are not affected; manage them with /plugin. Entries match skill names in user, project and local settings. The /skills menu saves to .claude/settings.local.json. The docs do not say whether changes apply in the middle of a session.

- Plugins: an enabled plugin is part of every session [S5]. Scopes: user (every project on this computer), project (committed .claude/settings.json, everyone in that repo), local (you, in that repo) [S5]. A plugin can be turned off with /plugin or `claude plugin disable`. The Installed tab's "Not used recently" group lists candidates. Official-marketplace plugins show a "Context cost" estimate [S5].
- The settings reference describes enabledPlugins as turning plugins "on or off per scope" [S3]. Whether a project file can switch off a plugin that is enabled at user scope is not stated in what I could read.
- Memory [S6]: CLAUDE.md files in parent folders load at launch. @imports expand at launch, up to four hops deep, and imports do not reduce cost. Rules without a paths: field load at launch. Path-scoped rules load only when Claude works with matching files. The docs target under 200 lines per CLAUDE.md. Auto memory loads its first 200 lines or 25 KB.
- MCP tool search is on by default. Only tool names and server instructions enter context. Full schemas load on demand [S7, read in an earlier session].
- Hooks [S4]:
  - UserPromptSubmit receives JSON on stdin. The prompt is the field named `prompt`; the docs example shows it. No environment variable carrying the prompt is documented. The crabin prototype reads an environment variable called CLAUDE_USER_PROMPT, which the docs do not define.
  - A UserPromptSubmit hook can add additionalContext alongside the prompt, or block the prompt. It cannot replace the prompt. No documented output field changes which skills or tools exist.
  - Each text output is capped at 10,000 characters. Larger output is saved to a file and replaced by a path plus a 2,000-character preview.
  - Default timeout for command hooks on this event: 30 seconds. The hooks page describes the timeout as "Seconds before canceling". If a hook times out, its output, including additionalContext, is discarded.
- Subagents: the sub-agents page says Claude Code warns at startup when subagent descriptions exceed 15,000 tokens [S26, read in an earlier session]. In this session's system prompt, the Agent tool lists each agent type with its description. That is an observation.
- Progressive disclosure: Anthropic's skills post says the name and description of every installed skill are preloaded at startup [S21, read in an earlier session].

## 3. OpenCode (docs and source)

- Skill locations: .opencode/skills/, ~/.config/opencode/skills/, plus the Claude-compatible .claude/skills/ and ~/.claude/skills/, and .agents/skills/ [S9]. The skill tool lists each skill's name and description. The body loads on demand [S9].
- permission.skill accepts allow, deny or ask, with wildcards such as internal-*, and per-agent overrides [S9].
- Source reading (anomalyco/opencode, read via gh api; I did not run OpenCode) [S10]:
  - skill/index.ts: `available()` drops any skill whose evaluated permission is deny.
  - session/system.ts: the skills block of the system prompt is skipped when skill is denied.
  - permission/index.ts `disabled()`: a tool counts as disabled only when its last matching rule is deny with pattern "*".
  - session/llm/request.ts `resolveTools`: disabled tools are filtered out of the tools object before the model call.
  - Practical result: "skill": "deny" removes the skill tool. "internal-*": "deny" hides only the matching skills and keeps the tool.
- Per agent: yes. Agent config accepts the same permission keys. A denied subagent is removed from the Task tool description [S9].
- MCP: enabled: false turns off a server. Glob keys disable tools, for example "my-mcp*": false, and can be set per agent [S9]. The docs do not say whether MCP definitions load lazily. I did not read that source.
- Plugin hooks: the docs list a few hook points. The Tarquinen plugin source uses more, so the real plugin surface is wider than the docs page shows [S15].

## 4. Codex (docs)

- Startup skill list: at most 2% of the context window, or 8,000 characters when the window size is unknown. Descriptions are shortened first [S8].
- Per-skill off switch: a [[skills.config]] entry with enabled = false in ~/.codex/config.toml (restart needed). allow_implicit_invocation: false in agents/openai.yaml stops automatic use [S8].

## 5. Projects that do this (read via gh api)

| Project (stars) | What it does | How it chooses | Verdict |
|---|---|---|---|
| crabin/ClaudeCodeSkillRouterPlugin (1) | Closest match: a UserPromptSubmit router for Claude Code skills [S11] | Keyword rules and substring scoring with a threshold; no model call. High confidence injects the full SKILL.md | Borrow the scoring and threshold. Problems: it reads CLAUDE_USER_PROMPT, which is not documented, so route.js would throw and probably route nothing (not run). It injects full bodies with no size cap |
| cK1NG22/locker-skill (0) [S12] | One stub skill that fetches skill content from a remote MCP server | The model calls fetch_skill or list_skills | The lazy-load idea works. But the content lives on someone else's server, so it is not for private local skills |
| zenobi-us/opencode-skillful (318; updated 2026-10-09) [S13] | OpenCode plugin with skill_find, skill_use and skill_resource | Skills load only when requested; the model has to search | An on-demand option for OpenCode, not automatic. Its header says 2 tools; its source defines 3 |
| anomalyco/opencode (212,386; official) [S10] | Host code for per-agent deny of skills and tools | Configuration, not routing | The mechanism Jordan can use inside OpenCode |
| dataware-co/metamcp (18; README only) [S17] | Three tools: mcp_discover, mcp_call, mcp_run. Common servers stay direct | Model-driven discovery | A good model for many MCP servers |
| jtznenic/mcp-proxy (0) [S16] | Static ALLOW_TOOLS and DENY_TOOLS filter on the tool list (lines 79-96, 136, 143) | Fixed lists | The simplest static pruning for MCP |
| exploreborders/claude-dcp (15) [S14] | Claude Code plugin: blocks duplicate tool calls, logs, compaction hooks, a token nudge (characters divided by 4) | Not a skill router | Solves a different cost, the growth of the conversation |
| Tarquinen/opencode-dynamic-context-pruning (4,328) [S15] | OpenCode plugin that compresses message history | Not a skill router | Complementary |
| metatool-ai/metamcp (2,699; README only) [S18] | MCP aggregator with namespaces | No lazy selection, per its README | Not a router |
| docker/mcp-gateway (1,590); codespriha/mcp-router-local (0) | Found in search, not read | n/a | Not assessed |

Published numbers (not reproduced by me):
- RAG-MCP (arXiv 2505.03275; Tiantian Gan and Qiyao Sun) [S19]: retrieval "significantly cuts prompt tokens (e.g., by over 50%)"; tool selection accuracy goes from 13.62% to 43.13%. The abstract credits these to the authors' experiments. I did not read the full paper.
- Anthropic Tool Search (published Nov 24, 2025) [S20]: in their example of 50+ MCP tools, about 77K tokens up front fell to about 8.7K (85% less). Opus 4 accuracy went from 49% to 74%. Opus 4.5 went from 79.5% to 88.1%.

No project I read is a tested Claude Code router. I did not run any of them.

## 6. Jordan's existing /d router (precedent)

- Skill: ~/.claude/skills/d/SKILL.md (v1.2.0). Hook: ~/.claude/hooks/jev-route-injector-userprompt.py [S23].
- The hook runs only when a prompt starts with /d. It runs scripts/jev-route.py and injects the result as additionalContext. It fails open. Its internal limit is 24 seconds, with 4 seconds per attempt. Its own comment says it is "NOT 100%% immunity" [S23].
- It is opt-in per prompt, not automatic. It is the closest working example of the injection contract on this machine.

## 7. Recommended design (needs Jordan's yes before any change)

Ordered by saving per unit of effort. Token figures are estimates from section 1.

Step 1. Keep large text out of every launch (no new code).
- Remove the @import of the agent-firefox SKILL.md from ~/.claude/CLAUDE.md. Keep its one-line safety rule (never Chrome) in that file. Saves about 1,300 tokens every launch.
- Move task-specific sections of X:\AI-2\becky-tools\CLAUDE.md (434 lines; the docs target 200) and X:\AI-2\CLAUDE.md into path-scoped rules that use a paths: field. Together those two files are about 14,000 tokens. How much of it is task-specific needs a read of each file.
- Add paths: fields to the 10 global rules files where they are area-specific. None has one today.
- Auto memory (13 KB) is under the cap. Prune stale entries if wanted.

Step 2. Shrink rarely used skills in the list (settings only).
- Set skillOverrides to "name-only" for rarely used skills. Their names stay visible and their descriptions are freed.
- "user-invocable-only" also removes the names. Use it only after the Step 3 router is tested. Otherwise Claude forgets these skills exist unless the router fires.
- Plugin skills cannot be set this way. Disable the whole plugin instead (Step 5).

Step 3. Deterministic router as a UserPromptSubmit hook (new code).
- Reads the prompt and cwd from the stdin JSON.
- Matches the prompt against an index of the rarely used skills: name, path, one-line description and keywords. Uses keyword or BM25 scoring with a threshold. No model call.
- On a match, injects the skill body if it is under about 8,000 characters. Otherwise injects the path and a short summary. This keeps it under the 10,000-character cap.
- Sets an explicit short timeout in the hook entry. The unit must be tested first (see section 8). Fails open.
- Logs every match and timeout, so precision can be checked against Jordan's real past prompts before it is switched on for everything.
- Routes once per user message. It does not route in the middle of a task.
- Token cost: zero when nothing matches, and the body's size when something does.
- Jordan's rule (2026-10-05, in ~/.claude/CLAUDE.md LESSONS): a new safeguard "ships as a hook or code path that runs by itself, never as a 'please do X' line in a skill/markdown file." The router must be a hook.

Step 4. Optional: System One as a second opinion.
- Call it only when the keyword score is unclear. It returns candidate skill names. Its answer is one input, merged with the keyword score. The main model still decides.
- Cost: one live test of 0.5 seconds and $0.000015 per call [S24, line 26]. Pricing is noted as $0.042 per million input tokens [S24, line 80]. That is one test, not a benchmark. The $5/month ledger in code caps spend. Never call it around the cap.
- Jordan's lessons (2026-10-08, ~/.claude/CLAUDE.md LESSONS): "A decision model is ONE signal, never the final say." Also: insert a model "INTO the proven path first", with "a new decision design comes second, side by side."

Step 5. Only if Jordan wants a whole plugin gone.
- Disable it with /plugin or `claude plugin disable`. To limit it to one project, install it at project or local scope instead.
- The largest candidate is everything-claude-code: 459 skills, 48 agents, 79 commands, 6 MCP servers, and 34.9% of enabled-plugin text. Disabling it removes all of that. That conflicts with Jordan's wish to keep those tools available, so it is his decision.

Measure: run /context in a fresh session before and after each step. I could not run it from here.

OpenCode: if Jordan uses OpenCode, per-agent permission.skill deny works. For automatic loading, zenobi-us/opencode-skillful is the nearest option. It still relies on the model choosing to search.

## 8. Risks

- Wrong pick: extra skill text costs tokens and can mislead. Mitigate with a threshold, one or two picks at most, a "possible match" label, a log, and a test against past prompts.
- Missed pick: no injection, so the skill is not used. Mitigate by keeping /name working (user-invocable skills) and by using the name-only step until the router is proven.
- Output over 10,000 characters is replaced by a path and a 2,000-character preview, so the body is not in context. The crabin prototype has this flaw.
- A timed-out hook discards its output, so the failure is silent. Log timeouts.
- Hook timeout unit conflict: the docs say seconds. Jordan's settings use values in the thousands; the /d hook has 26000, and its code comment says 26 seconds. If the docs are right, 26000 seconds is about 7.2 hours. The hook's own 24-second limit stops it from hanging in practice. This needs a test in a scratch project, not in Jordan's settings.
- Latency: the hook runs on every prompt. Python startup time is not measured. Keep the matcher small and the timeout short.
- Plugin copies drift from the plugin's own updates.
- Injected text becomes instructions to the model. Inject only Jordan's own files.
- Money: keyword matching costs nothing. Any model call must be System One inside the cap, or free.
- Approval: each step changes ~/.claude or CLAUDE.md files, which needs Jordan's yes.

## 9. Could not verify

1. The real in-context size of the skill listing (needs /context in a live session).
2. This session's context window, and therefore the budget size.
3. Whether the 1% budget also covers agent and command descriptions (the docs I read are silent).
4. Whether enabledPlugins merges key by key across scopes (the settings page was truncated).
5. Whether a project file can switch off a plugin that is enabled at user scope.
6. Whether a Skill(name) deny rule removes the skill from the listing. The docs say it blocks use; they do not say what happens to the listing.
7. Whether a per-tool MCP deny removes the tool from Claude Code's deferred list.
8. Whether skillOverrides changes apply in the middle of a session (not stated).
9. The unit of the hook timeout field in settings (conflict in section 8).
10. Whether the crabin router works (not run; it likely throws on the environment variable).
11. Whether OpenCode MCP definitions load lazily (the docs are silent; I did not read the source).
12. Whether the agent-firefox @import loads in full (the docs say imports load at launch; I did not observe a session).
13. All token figures are characters divided by 4.
14. The 61-name overlap does not prove duplicate content.
15. The user agent folder count is not clean (72 of 266 files have descriptions).
16. The text injected by the 7 prompt hooks is not measured.
17. The RAG-MCP and Anthropic figures are the publishers' own claims and were not reproduced.
18. Routing latency is not measured.

## 10. Notes

- Corrections to my earlier notes: there are 7 UserPromptSubmit hooks (not 8), and ruflo has 32 enabled plugins (not about 45).
- Four MCP servers failed to connect this session (Telegram, and three Zoom servers that returned HTTP 401). This research did not use them.
- Two other research agents on Jev topics were still running when I last checked. I did not read their output.
- Scratch scripts and JSON files are in the scratchpad only.

## Sources

S1 https://code.claude.com/docs/en/skills (listing budget, troubleshooting, disable-model-invocation, /context, /doctor, /skill-doctor; read in two parts)
S2 https://code.claude.com/docs/en/skills (skillOverrides section; read this session)
S3 https://code.claude.com/docs/en/settings-reference (index rows only; the page was truncated)
S4 https://code.claude.com/docs/en/hooks (UserPromptSubmit input, output, caps and timeouts; read in parts)
S5 https://code.claude.com/docs/en/plugins (plugin overview: every-session cost, scopes, Not used recently, Context cost; read this session)
S6 https://code.claude.com/docs/en/memory (CLAUDE.md loading, imports, rules, auto memory; read this session)
S7 https://code.claude.com/docs/en/mcp (tool search; read in an earlier session)
S8 https://learn.chatgpt.com/docs/build-skills (Codex; read in an earlier session)
S9 https://opencode.ai/docs/skills, /docs/agents, /docs/mcp-servers and /docs/plugins (read in an earlier session)
S10 https://github.com/anomalyco/opencode: packages/opencode/src/permission/index.ts (lines 204-218), src/skill/index.ts (around lines 310-317), src/session/system.ts (around lines 107-124), src/session/llm/request.ts (resolveTools, around line 210)
S11 https://github.com/crabin/ClaudeCodeSkillRouterPlugin (hooks/hooks.json, router/route.js, score-skills.js, classify-tags.js, inject-context.js)
S12 https://github.com/cK1NG22/locker-skill (locker.md)
S13 https://github.com/zenobi-us/opencode-skillful (src/index.ts)
S14 https://github.com/exploreborders/claude-dcp (hooks.json, context_nudge.py)
S15 https://github.com/Tarquinen/opencode-dynamic-context-pruning (src/index.ts)
S16 https://github.com/jtznenic/mcp-proxy (mcp_proxy.py)
S17 https://github.com/dataware-co/metamcp (README)
S18 https://github.com/metatool-ai/metamcp (README)
S19 https://arxiv.org/abs/2505.03275 (abstract; read this session)
S20 https://www.anthropic.com/engineering/advanced-tool-use (published Nov 24, 2025; read this session)
S21 https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills (read in an earlier session)
S22 Local: C:\Users\only1\.claude\settings.json (counts, names and timeouts only; no values printed)
S23 Local: C:\Users\only1\.claude\hooks\jev-route-injector-userprompt.py and C:\Users\only1\.claude\skills\d\SKILL.md
S24 Local: X:\AI-2\becky-tools\research\jev-integration-plan.md (lines 26 and 80)
S25 Local: scratchpad measure.json and measure_unique.json (the counts in this report)
S26 https://code.claude.com/docs/en/sub-agents (partial; read in an earlier session)
