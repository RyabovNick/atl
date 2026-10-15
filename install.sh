#!/bin/sh
# Installs atl on Linux or macOS without Go: downloads a release binary from
# GitLab, verifies its checksum, and runs `atl install`, which copies the binary
# to ~/.local/bin and sets up the opencode skill. Arguments are passed on to
# `atl install`, e.g. `sh install.sh --bin-dir /usr/local/bin`.
#
#   ATL_VERSION   release to install, e.g. v1.2.0 (default: the latest release)
#   GITLAB_TOKEN  a personal access token with read_api scope, for private projects
set -eu

# Maintainers: fill these in, or users set them in the environment.
ATL_GITLAB_URL="${ATL_GITLAB_URL:-}" # e.g. https://gitlab.example.com
ATL_PROJECT="${ATL_PROJECT:-}"       # project path, e.g. tools/atl

die() {
	echo "install.sh: $*" >&2
	exit 1
}

[ -n "$ATL_GITLAB_URL" ] || die "ATL_GITLAB_URL is not set (the GitLab address, e.g. https://gitlab.example.com)"
[ -n "$ATL_PROJECT" ] || die "ATL_PROJECT is not set (the project path, e.g. tools/atl)"
command -v curl >/dev/null || die "curl is required"
ATL_GITLAB_URL=${ATL_GITLAB_URL%/}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported system $(uname -s); on Windows download atl-windows-amd64.exe from the release page" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m)" ;;
esac

origin() { printf '%s\n' "$1" | sed -E 's#^([A-Za-z]+://[^/]+).*#\1#'; }
gitlab_origin=$(origin "$ATL_GITLAB_URL")

# fetch URL FILE follows redirects by hand so the token goes only to the GitLab
# host: package downloads may redirect to object storage, and curl would
# forward a custom header there. The token travels in curl's stdin config, not
# its arguments, so other users cannot read it from the process list.
fetch() {
	url=$1
	hops=0
	while :; do
		if [ -n "${GITLAB_TOKEN:-}" ] && [ "$(origin "$url")" = "$gitlab_origin" ]; then
			result=$(printf 'header = "PRIVATE-TOKEN: %s"\n' "$GITLAB_TOKEN" |
				curl -sS -K - -o "$2" -w '%{http_code} %{redirect_url}' "$url") || die "cannot reach $url"
		else
			result=$(curl -sS -o "$2" -w '%{http_code} %{redirect_url}' "$url") || die "cannot reach $url"
		fi
		status=${result%% *}
		next=${result#* }
		case $status in
		2??) return 0 ;;
		301 | 302 | 303 | 307 | 308)
			hops=$((hops + 1))
			[ "$hops" -le 5 ] && [ -n "$next" ] || die "too many or empty redirects from $1"
			url=$next
			;;
		401 | 403 | 404)
			if [ -n "${GITLAB_TOKEN:-}" ]; then
				die "HTTP $status from $url; check ATL_PROJECT, ATL_VERSION and that GITLAB_TOKEN has read_api scope"
			fi
			die "HTTP $status from $url; private projects need GITLAB_TOKEN (a personal access token with read_api scope)"
			;;
		*) die "HTTP $status from $url" ;;
		esac
	done
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

api="$ATL_GITLAB_URL/api/v4/projects/$(printf '%s' "$ATL_PROJECT" | sed 's#/#%2F#g')"
if [ -n "${ATL_VERSION:-}" ]; then
	tag=$ATL_VERSION
else
	fetch "$api/releases/permalink/latest" "$tmp/release.json"
	tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/release.json" | head -n 1)
	[ -n "$tag" ] || die "no release found for $ATL_PROJECT"
fi
version=${tag#v}
asset="atl-$os-$arch"

echo "Downloading atl v$version ($os/$arch)"
fetch "$api/packages/generic/atl/$version/$asset" "$tmp/$asset"
fetch "$api/packages/generic/atl/$version/SHA256SUMS" "$tmp/SHA256SUMS"

line=$(grep "  $asset\$" "$tmp/SHA256SUMS") || die "SHA256SUMS has no entry for $asset"
if command -v sha256sum >/dev/null; then
	(cd "$tmp" && printf '%s\n' "$line" | sha256sum -c - >/dev/null) || die "checksum mismatch for $asset"
else
	(cd "$tmp" && printf '%s\n' "$line" | shasum -a 256 -c - >/dev/null) || die "checksum mismatch for $asset"
fi

mv "$tmp/$asset" "$tmp/atl"
chmod +x "$tmp/atl"
"$tmp/atl" install "$@"
