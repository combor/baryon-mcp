#!/usr/bin/env bash
# Packs every binary goreleaser built into an MCPB bundle. Run after goreleaser,
# so the bundles carry the signed and notarized binaries:
#   mcpb-pack-all.sh
set -euo pipefail

cd "$(dirname "$0")/.."

version=$(jq -r .version dist/metadata.json)

# An MCPB manifest can only declare darwin, linux, or win32, so other targets
# ship as plain archives.
jq -r '.[] | select(.type == "Binary")
           | select(.goos == "darwin" or .goos == "linux" or .goos == "windows")
           | [.goos, .goarch, .path] | @tsv' dist/artifacts.json |
  while IFS=$'\t' read -r os arch path; do
    scripts/mcpb-pack.sh "$os" "$arch" "$path" "$version"
  done
