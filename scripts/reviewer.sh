#!/usr/bin/env bash
# Thin shim. The reviewer and its model pins live in opencodex-ops:
#   ~/opencodex/bin/codex-reviewer and ~/opencodex/lib/reviewer/reviewer-models.env
# Change models there, not here. This file only sets this repository's defaults;
# anything already set in the environment wins.
set -euo pipefail
export REVIEWER_DEFAULT_CLAUDE_ROLE="${REVIEWER_DEFAULT_CLAUDE_ROLE:-opus}"
reviewer="${CODEX_REVIEWER_BIN:-codex-reviewer}"
if ! command -v "$reviewer" >/dev/null 2>&1; then
  echo "Reviewer not installed: $reviewer is not on PATH (install opencodex-ops)." >&2
  exit 2
fi
exec "$reviewer" "$@"
