#!/usr/bin/env bash
# Regression test for get-becky-updates.ps1 (the "Get Becky Updates" one-click button).
#
# Drives the REAL script through every decision path using throwaway git repos and
# stubbed `go`/`claude` — no real Claude, no GPU, no network. Proves the button:
#   * auto-installs a clean update (even a non-fast-forward) by itself,
#   * tests the MERGED result and rolls back cleanly if it fails (never pushes a dud),
#   * hands off to the assistant ONLY for real conflicts / unfinished work, and always
#     launches it with --dangerously-skip-permissions (so it never prompts Jordan).
#
# Run it (needs Git Bash so `cygpath`/`powershell.exe` are available):
#   "C:\Program Files\Git\bin\bash.exe" get-becky-updates.test.sh
#
# Note: all SETUP commits go on side branches (never a branch literally named
# 'master') and the fake-claude log lives OUTSIDE the sandbox repo, so neither the
# local branch-protection hook nor a stray untracked file pollutes the test.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT_WIN="$(cygpath -w "$HERE/get-becky-updates.ps1")"
ROOT="$(mktemp -d)"
PASS=0; FAIL=0
say(){ printf '\n=== %s ===\n' "$1"; }
ok(){ printf '  PASS: %s\n' "$1"; PASS=$((PASS+1)); }
no(){ printf '  FAIL: %s\n' "$1"; FAIL=$((FAIL+1)); }

BIN="$ROOT/bin"; mkdir -p "$BIN"
printf '@echo off\r\nif "%%1"=="build" if "%%FAKE_GO_BUILD_FAIL%%"=="1" exit /b 1\r\nif "%%1"=="test" if "%%FAKE_GO_TEST_FAIL%%"=="1" exit /b 1\r\nexit /b 0\r\n' > "$BIN/go.cmd"
printf '@echo off\r\n>>"%%CLAUDE_LOG%%" echo CLAUDE_CALLED %%*\r\nexit /b 0\r\n' > "$BIN/claude.cmd"
BIN_WIN="$(cygpath -w "$BIN")"

mkc(){ printf '# CLAUDE.md\n\n## 6. Live handoff\n\n**Left for local agent:** %s\n\nMARKER: %s\n' "$1" "$2"; }

new_sandbox(){
  local d="$ROOT/$1"; rm -rf "$d"; mkdir -p "$d"
  git init --bare -q "$d/origin.git"
  git clone -q "$d/origin.git" "$d/work" 2>/dev/null
  ( cd "$d/work"
    git config user.email t@t; git config user.name t
    git config core.autocrlf false; git config core.eol lf
    git checkout -q -b seed
    mkdir -p becky-go; echo x > becky-go/keep.txt
    mkc "nothing — all merged." "base" > CLAUDE.md
    git add -A; git commit -qm "M0 base"
    git push -q origin seed:master
    git fetch -q origin
    git update-ref refs/heads/master refs/remotes/origin/master
  )
}

run_button(){ # workdir, FAKE_GO_BUILD_FAIL, FAKE_GO_TEST_FAIL
  local work="$1" bf="${2:-0}" tf="${3:-0}"
  local WORK_WIN; WORK_WIN="$(cygpath -w "$work")"
  local LOG; LOG="$(dirname "$work")/claude_log"; : > "$LOG"   # OUTSIDE the work repo
  local LOG_WIN; LOG_WIN="$(cygpath -w "$LOG")"
  CLAUDE_LOG_FILE="$LOG"
  OUT="$(powershell.exe -ExecutionPolicy Bypass -NoProfile -Command "\
\$env:PATH='$BIN_WIN;'+\$env:PATH; \
\$env:BECKY_REPO='$WORK_WIN'; \
\$env:CLAUDE_LOG='$LOG_WIN'; \
\$env:FAKE_GO_BUILD_FAIL='$bf'; \
\$env:FAKE_GO_TEST_FAIL='$tf'; \
& '$SCRIPT_WIN' -NoPause" 2>&1)"
}
claude_called(){ grep -q CLAUDE_CALLED "$CLAUDE_LOG_FILE" 2>/dev/null; }
claude_skipperm(){ grep -q -- '--dangerously-skip-permissions' "$CLAUDE_LOG_FILE" 2>/dev/null; }
st(){ ( cd "$1"; git status --porcelain ); }
rp(){ ( cd "$1"; git rev-parse "$2" ); }

# ---- A: clean non-fast-forward update should auto-install ----
say "A: clean non-fast-forward update should auto-install"
new_sandbox A; W="$ROOT/A/work"
( cd "$W"
  git checkout -q -b claude/clean master
  echo featureA > featureA.txt; git add -A; git commit -qm "C1 feature"
  git push -q origin claude/clean
  git checkout -q -b adv master
  echo localdoc > localdoc.txt; git add -A; git commit -qm "M1 local doc"
  git push -q origin adv:master
  git fetch -q origin
  git update-ref refs/heads/master refs/remotes/origin/master
  git checkout -q adv )
BASE_A=$(rp "$W" origin/master)
run_button "$W"
claude_called && no "assistant was launched (should not be)" || ok "assistant NOT launched (handled by script alone)"
[ "$(rp "$W" origin/master)" != "$BASE_A" ] && ok "origin/master advanced (update pushed)" || no "origin/master did not move"
[ -z "$(cd "$W"; git ls-remote origin claude/clean 2>/dev/null)" ] && ok "finished cloud branch deleted" || no "cloud branch not deleted"
[ -z "$(st "$W")" ] && ok "work tree left clean" || no "work tree dirty"
echo "$OUT" | grep -qi "update is installed" && ok "printed success banner" || no "no success banner"
( cd "$W"; git show origin/master:featureA.txt >/dev/null 2>&1 ) && ok "cloud-branch content merged into installed master" || no "cloud-branch file missing from master"
( cd "$W"; git show origin/master:localdoc.txt >/dev/null 2>&1 ) && ok "local content preserved in installed master" || no "local file missing from master"

# ---- B: conflicting update should roll back and hand to assistant ----
say "B: conflicting update should roll back and hand to assistant"
new_sandbox B; W="$ROOT/B/work"
( cd "$W"
  git checkout -q -b claude/conflict master
  mkc "nothing — all merged." "FROM-BRANCH" > CLAUDE.md
  git add -A; git commit -qm "branch edits MARKER"
  git push -q origin claude/conflict
  git checkout -q -b advb master
  mkc "nothing — all merged." "FROM-MASTER" > CLAUDE.md
  git add -A; git commit -qm "master edits same MARKER"
  git push -q origin advb:master
  git fetch -q origin
  git update-ref refs/heads/master refs/remotes/origin/master
  git checkout -q advb )
BASE_B=$(rp "$W" master)
run_button "$W"
claude_skipperm && ok "assistant launched WITH --dangerously-skip-permissions" || no "assistant not launched with skip-permissions"
[ "$(rp "$W" master)" = "$BASE_B" ] && ok "local master unchanged (merge aborted)" || no "local master changed after a conflict"
[ -z "$(st "$W")" ] && ok "work tree clean after abort (no conflict markers left)" || no "work tree left mid-conflict"
[ "$(rp "$W" origin/master)" = "$BASE_B" ] && ok "origin/master untouched" || no "origin/master changed on a conflict"

# ---- C: nothing new -> all caught up ----
say "C: nothing new -> all caught up, no assistant, no changes"
new_sandbox C; W="$ROOT/C/work"
BASE_C=$(rp "$W" master)
run_button "$W"
claude_called && no "assistant launched with no work" || ok "assistant NOT launched"
echo "$OUT" | grep -qi "caught up" && ok "printed 'All caught up'" || no "missing caught-up message"
[ "$(rp "$W" master)" = "$BASE_C" ] && ok "nothing changed" || no "master changed"

# ---- D: merged code fails tests -> roll back, hand to assistant, nothing pushed ----
say "D: merged code fails tests -> roll back, hand to assistant, nothing pushed"
new_sandbox D; W="$ROOT/D/work"
( cd "$W"
  git checkout -q -b claude/clean2 master
  echo f > featureD.txt; git add -A; git commit -qm "C1"
  git push -q origin claude/clean2
  git checkout -q -b advd master
  echo l > localD.txt; git add -A; git commit -qm "M1"
  git push -q origin advd:master
  git fetch -q origin
  git update-ref refs/heads/master refs/remotes/origin/master
  git checkout -q advd )
BASE_D=$(rp "$W" origin/master)
run_button "$W" 0 1
claude_skipperm && ok "assistant launched WITH skip-permissions" || no "assistant not launched on test failure"
[ "$(rp "$W" master)" = "$BASE_D" ] && ok "local master rolled back to base" || no "local master not rolled back"
[ "$(rp "$W" origin/master)" = "$BASE_D" ] && ok "origin/master NOT advanced (no dud pushed)" || no "a failing update got pushed"
[ -z "$(st "$W")" ] && ok "work tree clean after rollback" || no "work tree dirty after rollback"
[ -n "$(cd "$W"; git ls-remote origin claude/clean2 2>/dev/null)" ] && ok "cloud branch preserved (not deleted)" || no "cloud branch wrongly deleted"

# ---- E: section 6 says work is LEFT -> assistant, no merge ----
say "E: section 6 says work is LEFT -> assistant, no merge"
new_sandbox E; W="$ROOT/E/work"
( cd "$W"
  git checkout -q -b claude/unfinished master
  echo f > featureE.txt
  mkc "wire up the Python helper to the real model." "E" > CLAUDE.md
  git add -A; git commit -qm "C1 unfinished"
  git push -q origin claude/unfinished
  git checkout -q -b adve master
  echo l > localE.txt; git add -A; git commit -qm "M1"
  git push -q origin adve:master
  git fetch -q origin
  git update-ref refs/heads/master refs/remotes/origin/master
  git checkout -q adve )
BASE_E=$(rp "$W" master)
run_button "$W"
claude_skipperm && ok "assistant launched WITH skip-permissions" || no "assistant not launched"
[ "$(rp "$W" master)" = "$BASE_E" ] && ok "no merge attempted (master unchanged)" || no "master changed on unfinished work"

printf '\n============================================\n'
printf 'RESULT: %d passed, %d failed\n' "$PASS" "$FAIL"
printf '============================================\n'
rm -rf "$ROOT"
[ "$FAIL" -eq 0 ]
