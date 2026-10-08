#!/usr/bin/env bash
# A released migration never changes: a deployment has applied it already, so an edit reaches
# no database. Each schema change is a new file in db/migrations.
# The script compares each migration file of the latest release tag with the working tree.
set -euo pipefail
cd "$(dirname "$0")/.."
if ! tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null); then
  echo "checks/migrations.sh: no release tag found. Fetch the tags: git fetch --tags" >&2
  exit 1
fi
bad=0
while read -r file; do
  if [ ! -f "$file" ]; then
    echo "checks/migrations.sh: $file is in release $tag and must not be deleted" >&2
    bad=1
  elif ! git diff --quiet "$tag" -- "$file"; then
    echo "checks/migrations.sh: $file differs from release $tag. Add a new migration file." >&2
    bad=1
  fi
done < <(git ls-tree -r --name-only "$tag" -- db/migrations)
exit "$bad"
