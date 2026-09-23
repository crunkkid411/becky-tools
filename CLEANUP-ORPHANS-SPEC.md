# Cleanup Orphans Spec

**Status:** PENDING PEER REVIEW  
**Author:** Qwen Code  
**Date:** 2026-07-20  
**Location:** `X:\AI-2\becky-tools\cleanup-orphans.ps1`

---

## Problem Statement

When CLI tools (Claude Code, Qwen Code, etc.) are closed via the terminal X button, child processes (MCP servers, Node.js runtimes) become orphans and continue consuming RAM/CPU indefinitely. Users should not need to manually kill processes or remember special exit commands.

---

## Solution

Two PowerShell scripts:

### 1. `cleanup-orphans.ps1` (the killer)

Silently identifies and terminates **genuine orphan processes** while preserving legitimate long-running jobs.

**Safety rules (ALL must pass for a kill):**
1. Process age < 1 hour (preserves yt-dlp, scheduled automations)
2. Command line does NOT contain allowlisted patterns
3. Parent process no longer exists (true orphan detection)

**Allowlist patterns:**
- `yt-dlp` - Jordan's YouTube automation
- `claudeteam` - Claude Code infrastructure
- `firebase` / `emulator` - Firebase emulators
- `whoretana` - Becky-tools TTS agent
- `becky` - Becky-tools suite
- `mission-control` - Mission Control launcher
- `autopilot` / `playlist_ingest` - Scheduled automations

**Behavior:**
- Completely silent (no console output, no `pause`)
- No UI, no focus steal
- Exits cleanly with code 0

### 2. `install-cleanup-task.ps1` (optional installer)

Registers a Windows Scheduled Task that runs `cleanup-orphans.ps1` every 30 minutes.

**Task properties:**
- **Name:** `CLI-Orphan-Cleanup`
- **Trigger:** Every 30 minutes, indefinitely
- **Execution:** `PowerShell.exe -WindowStyle Hidden -ExecutionPolicy Bypass -File cleanup-orphans.ps1`
- **Timeout:** 2 minutes max
- **Principal:** Current user, highest privileges
- **Visibility:** Completely hidden (no window, no notification)

**Uninstall command:**
```powershell
Unregister-ScheduledTask -TaskName 'CLI-Orphan-Cleanup' -Confirm
```

---

## CLAUDE.md Compliance

### Section 4: Build & Test

| Requirement | Status |
|-------------|--------|
| ASCII-only (no em-dashes, smart quotes, Unicode) | PASS - verified via regex scan |
| PowerShell 5.1 parse-clean | PASS - validated via `Parser::ParseFile()` |
| No `.bat` wrapper needed (direct `.ps1` invocation) | N/A - designed for Task Scheduler |
| No `pause` (silent background operation) | PASS - exits cleanly |

### User Accessibility (Section 3)

| Requirement | Status |
|-------------|--------|
| No manual steps for Jordan | PASS - scheduled task is fully automatic |
| No focus steal / window popups | PASS - `-WindowStyle Hidden` |
| No screen reader / TTS needed | N/A - completely silent |
| Visual confirmation optional | TODO - could add optional log file |

### Lessons Canon (Section 4+)

| Lesson | Applied |
|--------|---------|
| "A kill isn't done until the UI shows it dead" | N/A - no UI; could add optional log |
| "Never direct Jordan to file paths or manual steps" | PASS - fully automatic |
| "Free or OAuth, nothing else" | PASS - no external APIs |
| "Check before launch" | PASS - script checks parent existence before kill |

---

## Risk Analysis

### What Could Go Wrong

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| False positive kill (legitimate process) | LOW - 3 independent checks | HIGH - breaks automation | 1-hour age threshold + allowlist + parent check |
| Scheduled task fails silently | MEDIUM | LOW - orphans accumulate | Manual script always available |
| PowerShell execution policy blocks | LOW | LOW - installer uses `-ExecutionPolicy Bypass` | Document manual invocation |
| Task Scheduler service disabled | LOW | LOW | Rare on desktop Windows |

### Conservative Design Choices

1. **1-hour age threshold** - All automations on this machine run for hours/days; anything < 1 hour old is suspect
2. **Allowlist-first** - Known-good patterns are excluded before any orphan logic
3. **Parent PID check** - Only kills if parent genuinely doesn't exist
4. **Silent operation** - No UI to steal focus or interrupt work

---

## Testing Plan

### Pre-merge Tests

```powershell
# 1. Parse validation (PowerShell 5.1)
$e=$null
[void][System.Management.Automation.Language.Parser]::ParseFile('cleanup-orphans.ps1',[ref]$null,[ref]$e)
$e.Count -eq 0  # Should be True

# 2. ASCII-only check
Get-Content cleanup-orphans.ps1 | ForEach-Object { 
    if ($_ -match '[^\x00-\x7F]') { throw "Non-ASCII at line $_" }
}

# 3. Dry-run (add Write-Host before kills, don't actually kill)
# TODO: Add -WhatIf flag support
```

### Post-merge Tests

1. **Run manually first** - Verify no false positives on known-good processes
2. **Spawn test orphan** - Start a Node process, kill parent, verify cleanup
3. **Install scheduled task** - Wait 30 min, verify ran silently
4. **Check Event Viewer** - Verify no errors in Task Scheduler logs

---

## Files

| File | Purpose |
|------|---------|
| `cleanup-orphans.ps1` | Main cleanup script |
| `install-cleanup-task.ps1` | Scheduled task installer |
| `CLEANUP-ORPHANS-SPEC.md` | This spec document |

---

## Peer Review Checklist

- [ ] **Safety:** Allowlist covers all legitimate long-running processes
- [ ] **Compliance:** Meets CLAUDE.md Section 4 requirements
- [ ] **Accessibility:** No manual steps, no focus steal
- [ ] **Testing:** Dry-run mode available before actual kills
- [ ] **Rollback:** Uninstall command documented
- [ ] **Logging:** Consider adding optional log file for debugging

---

## Approval Required

**DO NOT EXECUTE** until peer review completes and approval is granted.

**Reviewers:** Another AI agent will compare this spec against:
- `X:\AI-2\hj-mission-control\CLAUDE.md` (local standards)
- becky-tools existing patterns
- Safety requirements for Jordan's workflow

**After approval:**
1. Run manual test: `powershell -ExecutionPolicy Bypass -File cleanup-orphans.ps1`
2. If satisfied, install: `powershell -ExecutionPolicy Bypass -File install-cleanup-task.ps1`
3. Verify in Task Scheduler: `taskschd.msc` → Task Scheduler Library → CLI-Orphan-Cleanup
