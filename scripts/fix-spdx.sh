#!/usr/bin/env bash
# fix-spdx.sh — prepend the SPDX header (Apache-2.0; LicenseRef-ACKstorm-EE for ee/ and cmd/ach/ee.go) to any in-scope tracked
# *.go file missing it (same scope as pre-push gate 15: vendor/, .claude/,
# zz_generated*, mock_* are exempt). The gate scans the first 5 lines, so a
# plain prepend is gate-compliant; for build-tag-first files the resulting
# `// SPDX…` line is a leading line comment, which Go still permits before a
# `//go:build` constraint (the blank line separating them keeps the build
# constraint valid).
set -euo pipefail
fixed=0
while IFS= read -r f; do
  [[ "$f" == */vendor/* ]] && continue
  [[ "$f" == .claude/* ]] && continue
  [[ "$(basename "$f")" == zz_generated* ]] && continue
  [[ "$(basename "$f")" == mock_* ]] && continue
  HEADER="// SPDX-License-Identifier: Apache-2.0"
  [[ "$f" == ee/* || "$f" == cmd/ach/ee.go ]] && HEADER="// SPDX-License-Identifier: LicenseRef-ACKstorm-EE"
  if ! head -5 "$f" 2>/dev/null | grep -qx "$HEADER"; then
    tmp="$(mktemp)"
    if head -5 "$f" 2>/dev/null | grep -q '^// SPDX-License-Identifier:'; then
      # wrong identifier: replace it in place, never stack a second one
      awk -v h="$HEADER" '!d && NR<=5 && /^\/\/ SPDX-License-Identifier:/ {print h; d=1; next} {print}' "$f" > "$tmp"
    else
      printf '%s\n\n' "$HEADER" | cat - "$f" > "$tmp"
    fi
    cat "$tmp" > "$f"; rm -f "$tmp"
    echo "  +SPDX $f"
    fixed=$((fixed+1))
  fi
done < <(git ls-files '*.go')
echo "fix-spdx: ${fixed} file(s) updated"
