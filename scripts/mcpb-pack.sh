#!/usr/bin/env bash
# Packs one built binary into an MCPB bundle. Invoked by goreleaser post hooks:
#   mcpb-pack.sh <os> <arch> <binary-path> <version>
set -euo pipefail

os=$1 arch=$2 binary=$3 version=$4

# macOS ships only as the universal binary.
if [ "$os" = darwin ] && [ "$arch" != all ]; then
  exit 0
fi

cd "$(dirname "$0")/.."

case "$os" in
  darwin)  platform=darwin; suffix=darwin ;;
  linux)   platform=linux; suffix=linux-$arch ;;
  windows) platform=win32; suffix=windows-$arch ;;
  *) echo "unsupported os: $os" >&2; exit 1 ;;
esac

entry=server/baryon-mcp
[ "$os" = windows ] && entry=server/baryon-mcp.exe

dir=dist/mcpb/bundle-$suffix
rm -rf "$dir"
mkdir -p "$dir/server"
cp "$binary" "$dir/$entry"

jq --arg os "$platform" --arg entry "$entry" --arg ver "$version" \
  '.version = $ver
   | .compatibility.platforms = [$os]
   | .server.entry_point = $entry
   | .server.mcp_config.command = "${__dirname}/" + $entry' \
  manifest.json > "$dir/manifest.json"

npx --yes @anthropic-ai/mcpb@2.1.2 validate "$dir/manifest.json"
npx --yes @anthropic-ai/mcpb@2.1.2 pack "$dir" "dist/mcpb/baryon-mcp-$suffix.mcpb"
