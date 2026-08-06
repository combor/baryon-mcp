#!/usr/bin/env bash

set -euo pipefail

if [ "$#" -ne 3 ]; then
	printf 'usage: %s <version> <mcpb-directory> <output>\n' "${0##*/}" >&2
	exit 2
fi

VERSION=$1
MCPB_DIR=$2
OUTPUT=$3
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEMPLATE="$REPO_ROOT/server.json"

[ -n "$VERSION" ] || { printf 'version must not be empty\n' >&2; exit 2; }
[ -d "$MCPB_DIR" ] || { printf 'MCPB directory not found: %s\n' "$MCPB_DIR" >&2; exit 1; }
[ -d "$(dirname "$OUTPUT")" ] || { printf 'output directory not found: %s\n' "$(dirname "$OUTPUT")" >&2; exit 1; }

jq -e '.packages | type == "array" and length > 0 and all(.[]; .registryType == "mcpb")' \
	"$TEMPLATE" >/dev/null

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	else
		shasum -a 256 "$1" | awk '{ print $1 }'
	fi
}

repository_url=$(jq -er '.repository.url' "$TEMPLATE")
release_url="${repository_url%/}/releases/download/v$VERSION"
packages='[]'

while IFS= read -r package; do
	identifier=$(jq -er '.identifier' <<<"$package")
	filename=${identifier##*/}
	artifact="$MCPB_DIR/$filename"
	[ -f "$artifact" ] || { printf 'MCPB artifact not found: %s\n' "$artifact" >&2; exit 1; }
	hash=$(sha256_file "$artifact")
	package=$(jq -c \
		--arg identifier "$release_url/$filename" \
		--arg version "$VERSION" \
		--arg hash "$hash" \
		'.identifier = $identifier | .version = $version | .fileSha256 = $hash' \
		<<<"$package")
	packages=$(jq -c --argjson package "$package" '. + [$package]' <<<"$packages")
done < <(jq -c '.packages[]' "$TEMPLATE")

temporary=$(mktemp "${OUTPUT}.tmp.XXXXXX")
trap 'rm -f "$temporary"' EXIT
jq --arg version "$VERSION" --argjson packages "$packages" \
	'.version = $version | .packages = $packages' "$TEMPLATE" >"$temporary"
mv "$temporary" "$OUTPUT"
trap - EXIT
