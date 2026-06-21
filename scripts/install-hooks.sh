#!/usr/bin/env bash
# One-time: point this repo's git hooks at scripts/git-hooks (so the quality gate runs).
set -e
root="$(git rev-parse --show-toplevel)"
git -C "$root" config core.hooksPath scripts/git-hooks
chmod +x "$root/scripts/git-hooks/"* 2>/dev/null || true
echo "becky: git hooks installed (core.hooksPath = scripts/git-hooks). The pre-commit"
echo "quality gate now runs go build/vet/test + gofmt before every commit."
