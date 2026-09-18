#!/usr/bin/env bash
# check-launchers.sh - fail if any .bat/.ps1 launcher contains a non-ASCII byte.
#
# WHY THIS EXISTS: a single stray Unicode character (an em-dash, a smart quote)
# makes a BOM-less .ps1/.bat fail to PARSE under Windows PowerShell 5.1 - the
# console window flashes shut with no visible error. This has silently broken
# Jordan's one-click buttons repeatedly (Build Becky Clip.bat, Build Becky Drum.bat,
# get-becky-updates.ps1). ASCII-only launchers is a hard rule in CLAUDE.md; this
# script ENFORCES it in CI so the rule can never be quietly violated again.
#
# Scope: ONLY .bat/.ps1 (launcher) files. Markdown/Go may use Unicode freely.
# Exit 0 = all clean. Exit 1 = at least one launcher has a non-ASCII byte.
set -u

fail=0
while IFS= read -r f; do
  if LC_ALL=C grep -nP '[^\x00-\x7F]' "$f" >/dev/null 2>&1; then
    echo "NON-ASCII in launcher (breaks PowerShell 5.1 parsing): $f"
    LC_ALL=C grep -nP '[^\x00-\x7F]' "$f" | sed 's/^/    line /'
    fail=1
  fi
done < <(find . -path ./.git -prune -o \( -name '*.bat' -o -name '*.ps1' \) -print)

if [ "$fail" -ne 0 ]; then
  echo ""
  echo "FAIL: launcher scripts (.bat/.ps1) must be ASCII-only."
  echo "      Replace em-dashes with '-', curly/smart quotes with straight ' and \"."
  echo "      Reason + history: CLAUDE.md (the one-click-button parse-failure rule)."
  exit 1
fi

echo "OK: all .bat/.ps1 launchers are ASCII-only."

# --- PATH-wiping commands -------------------------------------------------------------
# WHY: an old becky-go build script printed a setx-on-PATH line with %PATH% appended as
# the "next step". Jordan ran it and it erased his whole user PATH (setx keeps at most
# 1,024 chars; in PowerShell it saves the literal text %PATH%). Repaired 2026-09-17; see
# HANDOFF-LOG.md. No script or README in this repo may print or run that command again,
# nor write the combined $env:Path back into a persistent PATH. Tools reach PATH because
# build-all-tools.bat copies them into C:\Users\only1\bin - nothing ever edits PATH.
# (The same patterns are blocked for local agents by ~/.claude/hooks/block-path-overwrite.py.)
setx_re='setx(\.exe)?[[:space:]]+(/m[[:space:]]+)?["'"'"']?path["'"'"']?([[:space:]]|$)'
envpath_re='SetEnvironmentVariable\([[:space:]]*["'"'"']path["'"'"'][^)]*\$env:path'
pathfail=0
while IFS= read -r -d '' f; do
  case "$f" in research/*) continue ;; esac
  hits="$(grep -niE -e "$setx_re" -e "$envpath_re" "$f" 2>/dev/null)"
  if [ -n "$hits" ]; then
    echo "PATH-WIPING COMMAND in: $f"
    echo "$hits" | sed 's/^/    line /'
    pathfail=1
  fi
done < <(git ls-files -z -- '*.bat' '*.cmd' '*.ps1' '*.psm1' '*.sh' '*.py' '*.go' 'README*' '*/README*')

if [ "$pathfail" -ne 0 ]; then
  echo ""
  echo "FAIL: a script or README tells someone to overwrite PATH (setx on PATH, or"
  echo "      \$env:Path written back to User/Machine). This erased Jordan's user PATH once."
  echo "      Copy tools into C:\\Users\\only1\\bin instead. History: HANDOFF-LOG.md 2026-09-18."
  exit 1
fi
echo "OK: no script or README overwrites PATH."
