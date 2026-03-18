#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
	echo "Usage: $0 <datadir> <backup-dir> [retention-count]" >&2
	exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATADIR="$1"
BACKUP_DIR="$2"
RETENTION="${3:-${UNIFIED_BACKUP_RETENTION:-7}}"

if [[ ! -d "$DATADIR" ]]; then
	echo "Datadir not found: $DATADIR" >&2
	exit 1
fi

if ! [[ "$RETENTION" =~ ^[0-9]+$ ]] || [[ "$RETENTION" -lt 1 ]]; then
	echo "Retention count must be a positive integer: $RETENTION" >&2
	exit 1
fi

mkdir -p "$BACKUP_DIR"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
ARCHIVE_PATH="$BACKUP_DIR/$(basename "$DATADIR")-$TIMESTAMP.tar.gz"

"$SCRIPT_DIR/backup_datadir.sh" "$DATADIR" "$ARCHIVE_PATH"

mapfile -t archives < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name "$(basename "$DATADIR")-*.tar.gz" -print | LC_ALL=C sort)

if (( ${#archives[@]} > RETENTION )); then
	for archive in "${archives[@]:0:${#archives[@]}-RETENTION}"; do
		rm -f "$archive"
		echo "Removed old backup $archive"
	done
fi

echo "Backup rotation complete. Retained ${RETENTION} archive(s) in $BACKUP_DIR"
