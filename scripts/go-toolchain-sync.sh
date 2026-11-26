#!/usr/bin/env bash
# Derives everything that must follow flake.nix's `goVersion` pin.
# Adapted from todaydeck's (itself from steadystate-mono).
#
#   * the `hash` of the Go source tarball the flake's Go override fetches
#   * go.mod's `go` directive
#
# Run it after bumping `goVersion` (`make go-toolchain-sync`); `make
# go-pin-check` fails if go.mod and the toolchain disagree.
#
# Needs only curl, jq and coreutils, NOT nix: a bumped goVersion with a
# stale hash can't build the dev shell that would otherwise fix it. go.dev's
# release index publishes each tarball's SHA-256, and a nix SRI hash is that
# same digest, base64-encoded.
set -euo pipefail

cd "$(dirname "$0")/.."

FLAKE=flake.nix
GOMOD=go.mod
INDEX='https://go.dev/dl/?mode=json&include=all'

fail() {
	echo "go-toolchain-sync: $*" >&2
	exit 1
}

# Anchored on the renovate marker comment so no other line can match.
version=$(sed -n 's/^[[:space:]]*goVersion = "\([^"]*\)";[[:space:]]*# renovate:.*/\1/p' "$FLAKE")
[ -n "$version" ] || fail "could not read goVersion from $FLAKE"

changed=0

# flake.nix: the src hash, on the line after its marker comment.
current_hash=$(awk '/go-toolchain-sync: hash/ { getline; print; exit }' "$FLAKE" |
	sed -n 's/.*hash = "\([^"]*\)";.*/\1/p')
[ -n "$current_hash" ] || fail "could not find the marked hash line in $FLAKE"

tarball="go${version}.src.tar.gz"
hex=$(curl -fsS "$INDEX" |
	jq -r --arg f "$tarball" '.[].files[] | select(.filename == $f) | .sha256' |
	head -1)
[ -n "$hex" ] || fail "go.dev's release index lists no $tarball; is goVersion a real release?"

# hex digest -> SRI (sha256-<base64>), coreutils only.
sri="sha256-$(printf '%s' "$hex" | sed 's/../\\x&/g' | xargs -0 printf '%b' | base64 | tr -d '\n')"

if [ "$sri" != "$current_hash" ]; then
	awk -v new="$sri" '
		/go-toolchain-sync: hash/ { print; getline; sub(/"sha256-[^"]*"/, "\"" new "\"") }
		{ print }
	' "$FLAKE" >"$FLAKE.tmp" && mv "$FLAKE.tmp" "$FLAKE"
	echo "go-toolchain-sync: $FLAKE src hash -> $sri"
	changed=1
fi

# go.mod: held equal to the toolchain, not merely <=. Under
# GOTOOLCHAIN=local a go.mod asking for more than the toolchain fails.
recorded=$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' "$GOMOD" | head -1)
[ -n "$recorded" ] || fail "could not read the go directive from $GOMOD"

if [ "$recorded" != "$version" ]; then
	sed -i "s/^go ${recorded}\$/go ${version}/" "$GOMOD"
	echo "go-toolchain-sync: $GOMOD go directive ${recorded} -> ${version}"
	changed=1
fi

[ "$changed" = 1 ] || echo "go-toolchain-sync: already in sync at go${version}"
