#!/usr/bin/env bash
# Prints code=false when BASE...HEAD only touches docs or version lines, so
# ci.yml can report ci-ok without running tests. Version lines are what
# `make release-bump` writes.
# ponytail: any `tag: v…` counts, so a v-tagged dependency bump in values.yaml
# skips tests too. Compare against Chart.yaml's version if that ever matters.
set -euo pipefail

base="$1"
head="$2"
code=false

while IFS= read -r f; do
  case "$f" in
    '' | *.md | docs/* | references/* | .planning/* | FIX*.txt | LICENSE | NOTICE | CODEOWNERS | .gitignore) ;;
    deploy/helm/ach/Chart.yaml | deploy/helm/ach/values.yaml | config/manager/kustomization.yaml)
      if git diff "$base...$head" -- "$f" |
        grep -E '^[-+]' | grep -vE '^(\+\+\+|---)' |
        grep -qvE '^[-+][[:space:]]*(version|appVersion|tag|newTag): v?[0-9]'; then
        code=true
      fi
      ;;
    *) code=true ;;
  esac
done < <(git diff --name-only "$base...$head")

echo "code=$code"
