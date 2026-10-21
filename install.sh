#!/bin/sh
# Install the pushrun server binary from GitHub releases — for machines
# without a Go toolchain.
#
#   curl -fsSL https://raw.githubusercontent.com/bunnyzr/pushrun/main/install.sh | sh
#   sh install.sh v1.2.3            # pin a version
#
# Honors: PUSHRUN_PREFIX (install prefix; default /usr/local, or ~/.local
# when /usr/local/bin is not writable).

set -eu

REPO="bunnyzr/pushrun"
VERSION="${1:-latest}"

need() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "install.sh: $1 is required but not installed" >&2
		exit 1
	}
}
need curl
need tar

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case $OS in
linux | darwin) ;;
*)
	echo "install.sh: unsupported OS $OS (want linux or darwin)" >&2
	exit 1
	;;
esac

ARCH=$(uname -m)
case $ARCH in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*)
	echo "install.sh: unsupported architecture $ARCH" >&2
	exit 1
	;;
esac

if [ "$VERSION" = latest ]; then
	VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p')
	[ -n "$VERSION" ] || {
		echo "install.sh: could not resolve the latest release" >&2
		exit 1
	}
fi
case $VERSION in
v*) ;;
*) VERSION="v$VERSION" ;;
esac
# Asset names carry the version without the leading v.
NUMVER=${VERSION#v}

ASSET="pushrun_${NUMVER}_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/$REPO/releases/download/$VERSION"

TMP=$(mktemp -d "${TMPDIR:-/tmp}/pushrun-install.XXXXXX")
trap 'rm -rf "$TMP"' EXIT
trap 'exit 1' HUP INT TERM

echo "install.sh: downloading $ASSET ($VERSION, $OS/$ARCH)"
curl -fsSL "$BASE/$ASSET" -o "$TMP/$ASSET"
curl -fsSL "$BASE/checksums.txt" -o "$TMP/checksums.txt"

cd "$TMP"
if command -v sha256sum >/dev/null 2>&1; then
	grep "  $ASSET\$" checksums.txt | sha256sum -c -
elif command -v shasum >/dev/null 2>&1; then
	grep "  $ASSET\$" checksums.txt | shasum -a 256 -c -
else
	echo "install.sh: warning: no sha256sum/shasum found; skipping checksum verification" >&2
fi

tar -xzf "$ASSET"

PREFIX=${PUSHRUN_PREFIX:-}
if [ -z "$PREFIX" ]; then
	if [ -w /usr/local/bin ]; then
		PREFIX=/usr/local
	else
		PREFIX="$HOME/.local"
	fi
fi
mkdir -p "$PREFIX/bin"
install -m 0755 pushrun "$PREFIX/bin/pushrun"

echo "install.sh: installed $PREFIX/bin/pushrun"
"$PREFIX/bin/pushrun" version
case ":$PATH:" in
*":$PREFIX/bin:"*) ;;
*) echo "install.sh: note: $PREFIX/bin is not in your PATH" >&2 ;;
esac
