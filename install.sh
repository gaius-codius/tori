#!/bin/sh
# Install or update tori from the latest GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/gaius-codius/tori/main/install.sh | sh
#
# Environment:
#   TORI_BINDIR   install directory (default ~/.local/bin)
#   TORI_VERSION  release tag to install (default: latest)
#   TORI_RELEASE_BASE  download base URL (mirrors, testing)
#   --uninstall   remove the installed binary
set -eu

REPO="gaius-codius/tori"
BINDIR="${TORI_BINDIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'tori install: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" = "--uninstall" ]; then
	rm -f "$BINDIR/tori"
	say "removed $BINDIR/tori"
	exit 0
fi

command -v curl >/dev/null || die "curl is required"
if command -v sha256sum >/dev/null; then
	sha256() { sha256sum "$@"; }
elif command -v shasum >/dev/null; then
	sha256() { shasum -a 256 "$@"; }
else
	die "sha256sum or shasum is required"
fi

case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "unsupported CPU: $(uname -m)" ;;
esac
asset="tori-$os-$arch"

if [ -n "${TORI_RELEASE_BASE:-}" ]; then
	base="$TORI_RELEASE_BASE"
elif [ -n "${TORI_VERSION:-}" ]; then
	base="https://github.com/$REPO/releases/download/$TORI_VERSION"
else
	base="https://github.com/$REPO/releases/latest/download"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "downloading $asset…"
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "no $asset in the release"
curl -fsSL -o "$tmp/SHA256SUMS.txt" "$base/SHA256SUMS.txt" || die "release has no SHA256SUMS.txt"
(cd "$tmp" && grep " $asset\$" SHA256SUMS.txt | sha256 -c --quiet -) || die "checksum mismatch"

mkdir -p "$BINDIR"
install -m755 "$tmp/$asset" "$BINDIR/tori"
say "installed $("$BINDIR/tori" version) to $BINDIR/tori"

case ":$PATH:" in
	*":$BINDIR:"*) ;;
	*) say "note: $BINDIR is not on your PATH; add it to run 'tori'" ;;
esac
command -v aria2c >/dev/null || say "note: install aria2 (aria2c) for downloads, e.g. 'sudo pacman -S aria2'"
