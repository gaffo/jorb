#!/usr/bin/env sh
# Point git at the repo's hook scripts (committed .githooks/).
set -eu
root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "Run this from inside the jorb git repository." >&2
  exit 1
}
cd "$root"
git config core.hooksPath .githooks
echo "git core.hooksPath is now .githooks (relative to repo root)."
