#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
	echo "Usage: $0 <datadir> [output-archive]" >&2
	exit 1
fi

DATADIR="$1"
if [[ ! -d "$DATADIR" ]]; then
	echo "Datadir not found: $DATADIR" >&2
	exit 1
fi

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUTPUT="${2:-}"
if [[ -z "$OUTPUT" ]]; then
	OUTPUT="$(pwd)/$(basename "$DATADIR")-$TIMESTAMP.tar.gz"
fi
PARENT_DIR="$(cd "$(dirname "$DATADIR")" && pwd)"
BASE_NAME="$(basename "$DATADIR")"
STAGING_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ufi-backup-XXXXXX")"
MANIFEST="$STAGING_DIR/manifest.json"

cat >"$MANIFEST" <<EOF
{
  "createdAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "sourceDatadir": "$DATADIR",
  "basename": "$BASE_NAME"
}
EOF

LC_ALL=C tar -czf "$OUTPUT" -C "$PARENT_DIR" "$BASE_NAME" -C "$STAGING_DIR" manifest.json
rm -rf "$STAGING_DIR"

echo "Backup written to $OUTPUT"
