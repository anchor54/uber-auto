#!/usr/bin/env bash
# Copies a folder of CI output (logs, screenshots) to the `ci-results` branch
# under runs/<run>-<sha>/<name>/ so results can be read with plain git.
# Usage: publish-results.sh <name> <folder>
set -euo pipefail

name=$1
src=$2
[ -d "$src" ] || { echo "nothing to publish in $src"; exit 0; }

dest="runs/${GITHUB_RUN_NUMBER}-${GITHUB_SHA::7}/${name}"
work=$(mktemp -d)
cd "$GITHUB_WORKSPACE"
git config user.name "ci-results-bot"
git config user.email "ci-results-bot@users.noreply.github.com"

for attempt in 1 2 3 4 5 6; do
  rm -rf "$work" && mkdir -p "$work"
  git worktree prune
  if git fetch --depth 1 origin ci-results 2>/dev/null; then
    git worktree add --detach "$work" FETCH_HEAD
  else
    git worktree add --detach "$work"
    (cd "$work" && git checkout -q --orphan ci-results-new && git rm -rfq . && echo "CI logs and screenshots, one folder per run." > README.md)
  fi
  mkdir -p "$work/$dest"
  cp -r "$src/." "$work/$dest/"
  echo "$dest" > "$work/LATEST-${name}"
  (
    cd "$work"
    git add -A
    git commit -qm "Results: $dest" || exit 0
    git push -q origin HEAD:refs/heads/ci-results
  ) && { echo "Published $dest"; exit 0; }
  sleep $((attempt * 4))
done
echo "Could not publish results" >&2
exit 1
