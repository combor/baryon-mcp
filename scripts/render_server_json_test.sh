#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/baryon-registry-test.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{ print $1 }'
	else
		shasum -a 256 "$1" | awk '{ print $1 }'
	fi
}

VERSION=9.8.7-test.1
MCPB_DIR="$TEST_DIR/mcpb"
OUTPUT="$TEST_DIR/server.json"
mkdir -p "$MCPB_DIR"

while IFS= read -r identifier; do
	filename=${identifier##*/}
	printf 'fixture for %s\n' "$filename" >"$MCPB_DIR/$filename"
done < <(jq -r '.packages[].identifier' "$REPO_ROOT/server.json")

"$REPO_ROOT/scripts/render_server_json.sh" "$VERSION" "$MCPB_DIR" "$OUTPUT"

jq -e --arg version "$VERSION" \
	'.version == $version and all(.packages[]; .version == $version)' \
	"$OUTPUT" >/dev/null || fail "version was not updated"

jq -S 'del(.version, .packages)' "$REPO_ROOT/server.json" >"$TEST_DIR/template-metadata.json"
jq -S 'del(.version, .packages)' "$OUTPUT" >"$TEST_DIR/output-metadata.json"
cmp -s "$TEST_DIR/template-metadata.json" "$TEST_DIR/output-metadata.json" \
	|| fail "non-release metadata changed"

while IFS= read -r package; do
	identifier=$(jq -r '.identifier' <<<"$package")
	filename=${identifier##*/}
	expected_url="https://github.com/combor/baryon-mcp/releases/download/v$VERSION/$filename"
	[ "$identifier" = "$expected_url" ] || fail "unexpected URL for $filename"
	expected_hash=$(sha256_file "$MCPB_DIR/$filename")
	actual_hash=$(jq -r '.fileSha256' <<<"$package")
	[ "$actual_hash" = "$expected_hash" ] || fail "unexpected hash for $filename"
done < <(jq -c '.packages[]' "$OUTPUT")

missing=$(jq -r '.packages[0].identifier | split("/")[-1]' "$REPO_ROOT/server.json")
rm "$MCPB_DIR/$missing"
if "$REPO_ROOT/scripts/render_server_json.sh" "$VERSION" "$MCPB_DIR" "$TEST_DIR/incomplete.json" \
	>"$TEST_DIR/incomplete.stdout" 2>"$TEST_DIR/incomplete.stderr"; then
	fail "missing MCPB artifact was accepted"
fi
grep -Fq "MCPB artifact not found: $MCPB_DIR/$missing" "$TEST_DIR/incomplete.stderr" \
	|| fail "missing artifact error was not actionable"

printf 'registry manifest tests passed\n'
