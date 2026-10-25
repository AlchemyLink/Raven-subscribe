#!/usr/bin/env bash
# Fail on govulncheck findings reachable from our code, except IDs accepted in
# .github/govulncheck-allow.txt. govulncheck has no ignore flag, so this reads
# its JSON stream: a finding whose trace starts at a function is reachable.
set -euo pipefail

allow=.github/govulncheck-allow.txt
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

govulncheck -format json ./... > "$tmp/out.json"
jq -r 'select(.finding != null and .finding.trace[0].function != null) | .finding.osv' "$tmp/out.json" \
  | sort -u > "$tmp/reachable"
{ grep -oE '^GO-[0-9]{4}-[0-9]+' "$allow" || true; } | sort -u > "$tmp/allowed"

blocking=$(comm -23 "$tmp/reachable" "$tmp/allowed")
if [ -n "$blocking" ]; then
  echo "::error::reachable vulnerabilities: $(echo "$blocking" | paste -sd' ')"
  govulncheck ./... || true
  exit 1
fi
for id in $(comm -12 "$tmp/reachable" "$tmp/allowed"); do
  echo "::warning::$id is reachable but accepted in $allow"
done
