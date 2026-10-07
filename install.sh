#!/bin/sh
# Install the sluice binary: download the release for this machine, check it against the
# published checksums, and put the binary on the PATH. The binary is the server, the runner
# and the CLI.
#
#   curl -fsSL https://raw.githubusercontent.com/alternayte/sluice/main/install.sh | sh
#
# Environment:
#   SLUICE_VERSION   the version to install, such as 0.3.0. The default is the latest release.
#   SLUICE_BIN_DIR   where the binary goes. The default is /usr/local/bin, or ~/.local/bin
#                    when /usr/local/bin needs a password and sudo is not there.
set -eu

REPO=alternayte/sluice

say() { printf '%s\n' "$*"; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is not installed. The installer needs curl, tar, and one of shasum or sha256sum."; }
need curl
need tar

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) die "Sluice has no release for $os. Run it in Docker: ghcr.io/$REPO." ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "Sluice has no release for $arch." ;;
esac

version=${SLUICE_VERSION:-}
if [ -z "$version" ]; then
	# The latest release by its tag. A prerelease, such as v1.2.0-rc.1, is never the latest.
	# The API answers without a token for a public repository.
	version=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$version" ] || die "The installer could not read the latest version. Set SLUICE_VERSION, or download from https://github.com/$REPO/releases."
fi
version=${version#v}

name="sluice_${version}_${os}_${arch}"
archive="$name.tar.gz"
base="https://github.com/$REPO/releases/download/v${version}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading sluice $version for $os/$arch."
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "$archive is not in release v$version."
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "The checksums of release v$version did not download."

# The checksum is the point of this script: nobody can trust a download that nobody checked.
say "Checking the download against the published checksum."
(
	cd "$tmp"
	grep " $archive\$" checksums.txt >expected.txt || exit 1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c expected.txt >/dev/null
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 -c expected.txt >/dev/null
	else
		exit 1
	fi
) || die "The download does not match its published checksum, or no checksum tool is installed. Nothing was installed."

tar -xzf "$tmp/$archive" -C "$tmp" "$name/sluice" || die "The archive holds no sluice binary."
chmod +x "$tmp/$name/sluice"

dir=${SLUICE_BIN_DIR:-}
sudo_needed=0
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then
		dir=/usr/local/bin
	elif command -v sudo >/dev/null 2>&1; then
		dir=/usr/local/bin
		sudo_needed=1
	else
		dir="$HOME/.local/bin"
	fi
fi
mkdir -p "$dir" 2>/dev/null || true

if [ "$sudo_needed" = "1" ]; then
	say "Installing to $dir. sudo asks for your password."
	sudo install -m 0755 "$tmp/$name/sluice" "$dir/sluice"
else
	install -m 0755 "$tmp/$name/sluice" "$dir/sluice" || die "The installer could not write to $dir. Set SLUICE_BIN_DIR to a directory you can write."
fi

say "sluice $version is in $dir."
case ":$PATH:" in
*":$dir:"*) ;;
*) say "Add $dir to your PATH." ;;
esac
say "To talk to a server, set SLUICE_URL and SLUICE_TOKEN, then run: sluice flows list"
