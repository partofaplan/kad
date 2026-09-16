#!/usr/bin/env bash
# Tests for next-version.sh, run against a throwaway git repo.
#
# Shell, not Go, because the thing under test is a shell script that CI calls
# directly. Getting this wrong mints an immutable tag and publishes a release
# under it, and neither can be moved afterwards.
set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
SCRIPT="$script_dir/next-version.sh"
fails=0

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work" || exit 1
git init -q .
git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init

check() {
  local tags=() kind exp got
  while [ "$1" != "--" ]; do tags+=("$1"); shift; done; shift
  kind=$1; exp=$2
  # shellcheck disable=SC2046
  git tag -d $(git tag) >/dev/null 2>&1 || true
  for t in "${tags[@]}"; do [ -n "$t" ] && git tag "$t"; done
  got=$("$SCRIPT" "$kind" 2>/dev/null || echo "ERR")
  if [ "$got" = "$exp" ]; then
    printf '  ok   %-30s %-12s -> %s\n' "[${tags[*]}]" "$kind" "$got"
  else
    printf '  FAIL %-30s %-12s -> got %q want %q\n' "[${tags[*]}]" "$kind" "$got" "$exp"
    fails=$((fails + 1))
  fi
}

echo "the three bump kinds"
check v1.0.0             -- minor   1.0.1   # merge into develop
check v1.0.1             -- major   1.1.0   # merge into main
check v1.4.7             -- release 2.0.0   # release package
check v1.0.0             -- major   1.1.0
check v2.3.9             -- minor   2.3.10

echo
echo "a repository with no tags starts at zero"
check ""                 -- minor   0.0.1
check ""                 -- major   0.1.0
check ""                 -- release 1.0.0

echo
echo "version ordering, not lexical"
check v0.9.9 v0.10.0     -- minor   0.10.1
check v1.9.0 v1.10.0     -- major   1.11.0
check v1.0.0 v2.0.0 v10.0.0 -- release 11.0.0
check v0.0.9 v0.0.10     -- minor   0.0.11

echo
echo "leading zeros are neither octal nor propagated"
# The component being INCREMENTED must not be read as octal, and the ones
# passed through must not keep their leading zero: SemVer2 forbids it.
check v1.08.0            -- minor   1.8.1
check v1.08.0            -- major   1.9.0
check v1.0.08            -- minor   1.0.9
check v1.08.9            -- release 2.0.0

echo
echo "release tags, for the notes range"
check v1.0.0 v1.2.3 v2.0.0 v2.1.0 -- last-release v2.0.0
check v1.2.3 v1.3.0      -- last-release ""
check ""                 -- last-release ""
check v0.0.1 v0.1.0 v1.0.0 -- first-tag v0.0.1

echo
echo "refusing to guess"
# Tags exist but none are parseable: restarting at 0.0.1 would move every
# published version backwards, so this must fail rather than guess.
check vfoo               -- minor   ERR
check v1.2               -- minor   ERR
check v1.2.3.4           -- minor   ERR
# A tag that already exists must never be re-minted.
check v1.0.0 v1.0.1      -- minor   1.0.2

echo
echo "bad usage"
if "$SCRIPT" nonsense >/dev/null 2>&1; then
  echo "  FAIL unknown kind was accepted"; fails=$((fails + 1))
else
  echo "  ok   unknown kind is rejected"
fi
if "$SCRIPT" >/dev/null 2>&1; then
  echo "  FAIL missing argument was accepted"; fails=$((fails + 1))
else
  echo "  ok   missing argument is rejected"
fi

echo
if [ "$fails" -ne 0 ]; then
  echo "$fails failure(s)"
  exit 1
fi
echo "all version arithmetic tests passed"
