#!/bin/sh
# Installs atl on Linux or macOS without Go: downloads a release binary from
# GitHub, verifies its checksum, and runs `atl install`, which copies the binary
# to ~/.local/bin and sets up the opencode skill. Arguments are passed on to
# `atl install`, e.g. `sh install.sh --bin-dir /usr/local/bin`.
#
#   ATL_VERSION   release to install, e.g. v1.2.0 (default: the latest release)
set -eu

repo=https://github.com/RyabovNick/atl

die() {
	echo "install.sh: $*" >&2
	exit 1
}

command -v curl >/dev/null || die "curl is required"

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported system $(uname -s); on Windows download atl-windows-amd64.exe from $repo/releases" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m)" ;;
esac

if [ -n "${ATL_VERSION:-}" ]; then
	base="$repo/releases/download/$ATL_VERSION"
else
	base="$repo/releases/latest/download"
fi
asset="atl-$os-$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading $asset ${ATL_VERSION:-(latest)}"
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "cannot download $base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "cannot download $base/SHA256SUMS"

line=$(grep "  $asset\$" "$tmp/SHA256SUMS") || die "SHA256SUMS has no entry for $asset"
if command -v sha256sum >/dev/null; then
	(cd "$tmp" && printf '%s\n' "$line" | sha256sum -c - >/dev/null) || die "checksum mismatch for $asset"
else
	(cd "$tmp" && printf '%s\n' "$line" | shasum -a 256 -c - >/dev/null) || die "checksum mismatch for $asset"
fi

mv "$tmp/$asset" "$tmp/atl"
chmod +x "$tmp/atl"
"$tmp/atl" install "$@"
