#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "Usage: $0 <backup-archive> <target-datadir>" >&2
	exit 1
fi

ARCHIVE="$1"
TARGET="$2"

if [[ ! -f "$ARCHIVE" ]]; then
	echo "Backup archive not found: $ARCHIVE" >&2
	exit 1
fi

if [[ -e "$TARGET" && -n "$(find "$TARGET" -mindepth 1 -maxdepth 1 2>/dev/null | head -n 1)" ]]; then
	echo "Target directory must be empty: $TARGET" >&2
	exit 1
fi

TMPDIR_RESTORE="$(mktemp -d "${TMPDIR:-/tmp}/ufi-restore-XXXXXX")"
trap 'rm -rf "$TMPDIR_RESTORE"' EXIT

LC_ALL=C tar -xzf "$ARCHIVE" -C "$TMPDIR_RESTORE"

SOURCE_DIR="$(find "$TMPDIR_RESTORE" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
if [[ -z "$SOURCE_DIR" ]]; then
	echo "Backup archive did not contain a datadir" >&2
	exit 1
fi

mkdir -p "$TARGET"
rm -rf "$TARGET"
cp -R "$SOURCE_DIR" "$TARGET"

echo "Datadir restored to $TARGET"
