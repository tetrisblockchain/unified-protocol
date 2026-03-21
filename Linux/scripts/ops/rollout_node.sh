#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DATADIR="${UFI_DATADIR:-}"
PID_FILE="${UFI_PID_FILE:-}"
RESTART="${UFI_RESTART:-0}"
LOG_FILE="${UFI_LOG_FILE:-$ROOT_DIR/logs/unified-node-rollout.log}"

cd "$ROOT_DIR"
make test build

if [[ -n "$DATADIR" ]]; then
	"$ROOT_DIR/scripts/ops/backup_datadir.sh" "$DATADIR"
fi

if [[ "$RESTART" != "1" ]]; then
	echo "Build and backup complete. Set UFI_RESTART=1 to restart the node automatically."
	exit 0
fi

if [[ $# -eq 0 ]]; then
	echo "Provide unified-node flags after the script when using UFI_RESTART=1." >&2
	exit 1
fi

if [[ -n "$PID_FILE" && -f "$PID_FILE" ]]; then
	PID="$(cat "$PID_FILE")"
	if [[ -n "$PID" ]] && kill -0 "$PID" >/dev/null 2>&1; then
		kill "$PID"
		wait "$PID" 2>/dev/null || true
	fi
fi

mkdir -p "$(dirname "$LOG_FILE")"
nohup "$ROOT_DIR/build/unified-node" "$@" >>"$LOG_FILE" 2>&1 &
NEW_PID=$!

if [[ -n "$PID_FILE" ]]; then
	mkdir -p "$(dirname "$PID_FILE")"
	echo "$NEW_PID" >"$PID_FILE"
fi

echo "Node restarted with PID $NEW_PID"
echo "Log file: $LOG_FILE"
