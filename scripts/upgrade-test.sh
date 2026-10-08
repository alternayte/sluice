#!/usr/bin/env bash
# Run the upgrade test: the binary of the last release and bin/sluice on one database.
# The script downloads the release binary into build/upgrade and checks its checksum.
# Build bin/sluice first: just build-ui build-go.
set -euo pipefail
cd "$(dirname "$0")/.."
# HEAD^ so that the commit of a release tag upgrades from the release before it.
tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*' HEAD^)
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64) arch=arm64 ;;
esac
name="sluice_${tag#v}_${os}_${arch}"
dir=build/upgrade
mkdir -p "$dir"
if [ ! -x "$dir/$name/sluice" ]; then
  base="https://github.com/alternayte/sluice/releases/download/$tag"
  curl -fsSL -o "$dir/$name.tar.gz" "$base/$name.tar.gz"
  curl -fsSL -o "$dir/checksums-$tag.txt" "$base/checksums.txt"
  want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f {print $1}' "$dir/checksums-$tag.txt")
  if command -v sha256sum >/dev/null; then got=$(sha256sum "$dir/$name.tar.gz" | cut -d' ' -f1); else got=$(shasum -a 256 "$dir/$name.tar.gz" | cut -d' ' -f1); fi
  if [ -z "$want" ] || [ "$want" != "$got" ]; then
    echo "upgrade-test: checksum of $name.tar.gz does not match checksums.txt of $tag" >&2
    exit 1
  fi
  tar -C "$dir" -xzf "$dir/$name.tar.gz"
fi
echo "upgrade-test: from $tag ($dir/$name/sluice) to bin/sluice"
SLUICE_UPGRADE_FROM_BINARY="$PWD/$dir/$name/sluice" SLUICE_UPGRADE_FROM_VERSION="$tag" SLUICE_E2E_BINARY="$PWD/bin/sluice" \
  go test -tags 'e2e upgrade' -count=1 -timeout 15m -run '^TestUpgradeFromLastRelease$' -v ./tests/e2e/
