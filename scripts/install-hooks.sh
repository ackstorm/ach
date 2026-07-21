#!/usr/bin/env bash
# Install git hooks for ach.
#
# Installs:
#   pre-push   -> scripts/pre-push-check.sh
#     Runs the full 17-gate pre-publication check before every `git push`.
#     Includes gitleaks/trufflehog/SPDX/govulncheck plus the defensive
#     full-sweep golangci-lint + make unit.
#
# Idempotent — safe to re-run.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "not inside a git repo" >&2; exit 1;
}
cd "$REPO_ROOT"

HOOKS_DIR="$(git rev-parse --git-path hooks)"
mkdir -p "$HOOKS_DIR"

MARKER="# ach:managed-hook"

install_hook() {
  local name="$1" script_rel="$2"
  local hook_path="${HOOKS_DIR}/${name}"
  if [[ ! -x "$script_rel" ]]; then
    echo "$script_rel missing or not executable" >&2
    return 1
  fi
  # Back up any prior hook this installer doesn't already own — covers both
  # a foreign pre-existing hook and the historical `ln -sf` symlink install.
  if [[ -e "$hook_path" || -L "$hook_path" ]] && ! grep -qF "$MARKER" "$hook_path" 2>/dev/null; then
    local backup="${hook_path}.bak.$(date -u +%Y%m%dT%H%M%SZ)"
    mv "$hook_path" "$backup"
    echo "backed up existing $hook_path -> $backup"
  fi
  # A managed WRAPPER, not a symlink. `.git/hooks` is shared across every
  # linked worktree, but a plain `ln -sf ../../<script>` resolves relative to
  # that shared hooks dir — i.e. always into the PRIMARY checkout's copy of
  # the script, even when a linked worktree on a different branch is the one
  # running `git push`. That copy can be stale (missing a fix only committed
  # on the worktree's own branch) or simply wrong. Resolving
  # `git rev-parse --show-toplevel` INSIDE the hook instead picks the
  # invoking worktree's own checkout every time, so it always runs the
  # script version actually committed on the branch being pushed.
  cat > "$hook_path" <<EOF
#!/usr/bin/env bash
$MARKER — installed by scripts/install-hooks.sh; re-run \`make hooks\` to refresh.
set -euo pipefail
root="\$(git rev-parse --show-toplevel)"
exec "\${root}/${script_rel}" "\$@"
EOF
  chmod +x "$hook_path"
  echo "installed: $hook_path -> \$(git rev-parse --show-toplevel)/${script_rel} (resolved per-worktree at push time)"
}

# Remove any stale pre-commit hook from a prior install (pre-commit gate retired).
stale_pc="${HOOKS_DIR}/pre-commit"
if [[ -e "$stale_pc" || -L "$stale_pc" ]]; then
  rm -f "$stale_pc"
  echo "removed stale pre-commit hook: $stale_pc"
fi

install_hook pre-push   "scripts/pre-push-check.sh"
