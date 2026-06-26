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
