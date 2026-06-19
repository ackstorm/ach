#!/usr/bin/env bash
# fix-spdx.sh — prepend the Apache-2.0 SPDX header to any in-scope tracked
# *.go file missing it (same scope as pre-push gate 15: vendor/, .claude/,
# zz_generated*, mock_* are exempt). The gate scans the first 5 lines, so a
# plain prepend is gate-compliant; for build-tag-first files the resulting
# `// SPDX…` line is a leading line comment, which Go still permits before a
# `//go:build` constraint (the blank line separating them keeps the build
# constraint valid).
set -euo pipefail
HEADER="// SPDX-License-Identifier: Apache-2.0"
fixed=0
while IFS= read -r f; do
  [[ "$f" == */vendor/* ]] && continue
  [[ "$f" == .claude/* ]] && continue
  [[ "$(basename "$f")" == zz_generated* ]] && continue
  [[ "$(basename "$f")" == mock_* ]] && continue
  if ! head -5 "$f" 2>/dev/null | grep -qx "$HEADER"; then
    tmp="$(mktemp)"
    printf '%s\n\n' "$HEADER" | cat - "$f" > "$tmp"
    mv "$tmp" "$f"
    echo "  +SPDX $f"
    fixed=$((fixed+1))
  fi
done < <(git ls-files '*.go')
echo "fix-spdx: ${fixed} file(s) updated"
