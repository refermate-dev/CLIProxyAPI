#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/reviewer-wrapper-test.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT
mkdir -p "$TMP_ROOT/home"
export HOME="$TMP_ROOT/home"
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
for key in \
  ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN ANTHROPIC_BASE_URL ANTHROPIC_CUSTOM_HEADERS \
  CLAUDE_REVIEWER_EFFORT CLAUDE_REVIEWER_MODEL CLAUDE_REVIEWER_TOOLS \
  CODEX_REVIEWER_MODEL CODEX_REVIEWER_MODEL_FALLBACK CODEX_REVIEWER_REASONING_EFFORT \
  FABLE_REVIEW_FILES_MAX_DIFF_LINES REVIEWER_ATTEMPTS REVIEWER_CLIPROXY \
  REVIEWER_CLIPROXY_CONFIG REVIEWER_DRY_RUN REVIEWER_TIMEOUT_SECONDS \
  REVIEW_FILES_REVIEWER_ATTEMPTS REVIEW_FILES_REVIEWER_TIMEOUT_SECONDS \
  SOURCE_CODEX_HOME; do
  unset "$key"
done

repo="$TMP_ROOT/repo"
bin_dir="$TMP_ROOT/bin"
auth_home="$TMP_ROOT/codex-home"
mkdir -p "$repo" "$bin_dir" "$auth_home"
printf '{}\n' > "$auth_home/auth.json"

git -C "$repo" init -q
git -C "$repo" config user.email reviewer@example.com
git -C "$repo" config user.name 'Reviewer Test'
printf 'baseline\n' > "$repo/baseline.txt"
git -C "$repo" add baseline.txt
git -C "$repo" commit -qm baseline

cat > "$bin_dir/codex" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == debug && "${2:-}" == models ]]; then
  printf '%s\n' '{"models":[{"slug":"gpt-6-astra","visibility":"list","supported_in_api":true,"upgrade":null},{"slug":"gpt-5.6-sol","visibility":"list","supported_in_api":true,"upgrade":null}]}'
  exit 0
fi
if [[ "${1:-}" == exec ]]; then
  sed -n '1,20p' "$CODEX_HOME/config.toml"
  cat
  exit 0
fi
exit 2
STUB

cat > "$bin_dir/claude" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
cat
STUB
chmod +x "$bin_dir/codex" "$bin_dir/claude"
bash -n "$ROOT/scripts/reviewer.sh"

printf 'changed scope\n' > "$repo/reviewed file.txt"
printf 'must stay private\n' > "$repo/unlisted-secret.txt"
review_output="$(
  cd "$repo"
  PATH="$bin_dir:$PATH" SOURCE_CODEX_HOME="$auth_home" REVIEWER_CLIPROXY=0 \
    "$ROOT/scripts/reviewer.sh" review-files --worktree -- 'reviewed file.txt'
)"
grep -q 'model = "gpt-' <<< "$review_output"
grep -q 'reviewed file.txt' <<< "$review_output"
grep -q 'changed scope' <<< "$review_output"
if grep -q 'must stay private' <<< "$review_output"; then
  echo 'review-files leaked an unlisted file' >&2
  exit 1
fi

if (
  cd "$repo"
  REVIEWER_DRY_RUN=1 REVIEWER_CLIPROXY=0 \
    "$ROOT/scripts/reviewer.sh" review-files --worktree -- missing.txt
) > "$TMP_ROOT/missing.out" 2> "$TMP_ROOT/missing.err"; then
  echo 'review-files accepted an empty path scope' >&2
  exit 1
fi
grep -q 'No worktree diff found' "$TMP_ROOT/missing.err"

printf 'included line\nexcluded line\n' > "$repo/lines.txt"
line_output="$(
  cd "$repo"
  PATH="$bin_dir:$PATH" REVIEWER_CLIPROXY=0 CLAUDE_REVIEWER_MODEL=claude-opus-5 \
    "$ROOT/scripts/reviewer.sh" review-lines --reviewer claude -- 'lines.txt:1'
)"
grep -q 'included line' <<< "$line_output"
if grep -q 'excluded line' <<< "$line_output"; then
  echo 'review-lines leaked an unselected line' >&2
  exit 1
fi

printf 'OUTSIDE_SECRET_SENTINEL\n' > "$TMP_ROOT/outside.txt"
ln -s "$TMP_ROOT/outside.txt" "$repo/outside-link.txt"
if (
  cd "$repo"
  REVIEWER_DRY_RUN=1 REVIEWER_CLIPROXY=0 \
    "$ROOT/scripts/reviewer.sh" review-files --worktree -- outside-link.txt
) > "$TMP_ROOT/symlink.out" 2> "$TMP_ROOT/symlink.err"; then
  echo 'review-files accepted an out-of-repo symlink' >&2
  exit 1
fi
grep -q 'must resolve inside the repo' "$TMP_ROOT/symlink.err"
if grep -q 'OUTSIDE_SECRET_SENTINEL' "$TMP_ROOT/symlink.out" "$TMP_ROOT/symlink.err"; then
  echo 'symlink target content escaped into reviewer output' >&2
  exit 1
fi

doctor_output="$(
  PATH="$bin_dir:$PATH" SOURCE_CODEX_HOME="$auth_home" REVIEWER_CLIPROXY=0 \
    "$ROOT/scripts/reviewer.sh" doctor --reviewer all
)"
grep -q 'codex reviewer:' <<< "$doctor_output"
grep -q 'claude reviewer:' <<< "$doctor_output"

printf 'reviewer wrapper smoke passed\n'
