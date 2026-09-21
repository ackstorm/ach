#!/usr/bin/env bash
# Set a mike alias, tolerating "already exists". Usage: mike-alias.sh <alias> <version>
set -u
alias_name="$1"; version="$2"
echo "🔧 Setting alias '$alias_name' to version '$version'"
if mike alias --push "$version" "$alias_name" 2>/dev/null; then
  echo "✅ Successfully set alias '$alias_name' to '$version'"
else
  echo "⚠️  Alias '$alias_name' already exists or failed to set"
fi
