#!/usr/bin/env bash
# Confirm every pinned chart version in the catalog still resolves upstream.
#
# This is deliberately NOT part of 'make test'. It needs the network and a
# working helm, and the test suite must pass on a machine that has neither —
# that is the boundary the SDLC in .claude/SKILL.md is built on.
#
# The pins are read back out of the binary rather than restated here; a second
# copy of them would drift from the first.
#
# --repo, not 'helm repo add': this must not leave entries behind in the
# developer's ~/.config/helm, for the same reason kad itself does not.
set -uo pipefail

fail=0
echo "Verifying pinned chart versions:"
echo

while IFS=$'\t' read -r name chart version repo; do
  [ -z "${name:-}" ] && continue
  printf '  %-15s %-28s %-12s ' "$name" "$chart" "$version"

  args=(--version "$version")
  if [ "${repo#oci://}" != "$repo" ]; then
    args+=("${repo%/}/${chart}")
  else
    args+=(--repo "$repo" "$chart")
  fi

  if out=$(helm show chart "${args[@]}" 2>&1); then
    echo "ok"
  else
    echo "UNRESOLVED"
    echo "      ${out%%$'\n'*}" >&2
    fail=1
  fi
done < <(./bin/kad catalog -o tsv)

echo
if [ "$fail" -ne 0 ]; then
  echo "One or more pinned chart versions did not resolve." >&2
  echo "Bump them in internal/catalog/catalog.go and re-run." >&2
  exit 1
fi
echo "All pins resolve."
