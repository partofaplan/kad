#!/usr/bin/env bash
# Computes the next version under the RELEASE.MAJOR.MINOR scheme, from git tags.
#
#   minor    merge into develop      R.M.m -> R.M.(m+1)
#   major    merge into main         R.M.m -> R.(M+1).0
#   release  a release package       R.M.m -> (R+1).0.0
#
# Also:
#   last-release   prints the newest release tag (vR.0.0), or nothing if none
#   first-tag      prints the oldest tag, for release notes with no prior release
#
# Tags are the source of truth, so there is no VERSION file to drift. This lives
# in one script rather than inline in each workflow: ci.yml and release.yml both
# need this arithmetic, and two copies would eventually disagree about what
# "next" means.
#
# Adapted from kado-operator, minus its two-place legacy tag migration — this
# repository has only ever used three-place tags, so that branch would be dead
# code carrying a real risk: it only fires when the three-place lookup finds
# nothing, which is exactly the situation where guessing is most dangerous.
set -euo pipefail

kind=${1:-}
case "$kind" in
  minor|major|release|last-release|first-tag) ;;
  *) echo "usage: $0 <minor|major|release|last-release|first-tag>" >&2; exit 2 ;;
esac

# Three ways "no tags" can be a lie rather than a fact, each of which would
# restart numbering at 0.0.1 and move every published version backwards.
#
# 1. Not a git repository at all.
git rev-parse --git-dir >/dev/null 2>&1 \
  || { echo "::error::not a git repository; refusing to guess a version" >&2; exit 1; }

# 2. A shallow clone, which can have no tags while the remote has many. Both
#    callers use fetch-depth: 0, but that is one workflow edit away from
#    changing, and the failure would be silent.
if [ "$(git rev-parse --is-shallow-repository)" = "true" ]; then
  echo "::error::shallow clone: tags may be missing, so any version computed here could move backwards. Use fetch-depth: 0." >&2
  exit 1
fi

# 3. A git failure of any other kind. No `|| true` here: an error must not read
#    as an empty tag list.
tags=$(git tag --list 'v*')

# A release tag is one where both MAJOR and MINOR are zero. That is what makes
# "since the last release" answerable without a separate marker.
last_release=$(printf '%s\n' "$tags" | grep -E '^v[0-9]+\.0\.0$' | sort -V | tail -n1 || true)
if [ "$kind" = "last-release" ]; then
  printf '%s\n' "$last_release"
  exit 0
fi

# The oldest tag, used as the notes range for the FIRST release, when there is
# no previous release to measure from.
if [ "$kind" = "first-tag" ]; then
  printf '%s\n' "$(printf '%s\n' "$tags" | sort -V | head -n1 || true)"
  exit 0
fi

three=$(printf '%s\n' "$tags" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n1 || true)
any=$(printf '%s\n' "$tags"   | grep -cE '^v' || true)

if [ -n "$three" ]; then
  latest=${three#v}
elif [ "${any:-0}" -gt 0 ]; then
  # Numbering would otherwise silently restart at 0.0.1 and move backwards.
  echo "::error::v* tags exist but none match vR.M.m; refusing to restart numbering" >&2
  exit 1
else
  latest="0.0.0"
fi

IFS=. read -r rel maj min <<<"$latest"

# Every component goes through 10#, not just the one being incremented. A tag
# like v1.08.0 would otherwise be read as octal and abort with "value too great
# for base"; and passing an un-incremented component through verbatim would
# propagate its leading zero into the new version, producing something like
# 1.08.1 — which SemVer2 forbids, and which would be rejected downstream.
rel=$((10#$rel)); maj=$((10#$maj)); min=$((10#$min))

case "$kind" in
  minor)   next="${rel}.${maj}.$((min + 1))" ;;
  major)   next="${rel}.$((maj + 1)).0" ;;
  release) next="$((rel + 1)).0.0" ;;
esac

[[ "$next" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
  || { echo "::error::computed bad version '$next' from '$latest'" >&2; exit 1; }

if git rev-parse -q --verify "refs/tags/v${next}" >/dev/null; then
  echo "::error::tag v${next} already exists" >&2
  exit 1
fi

printf '%s\n' "$next"
