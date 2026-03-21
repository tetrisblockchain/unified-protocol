#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

SERVICE_NAME="${UNIFIED_BACKUP_SERVICE_NAME:-unified-backup}"
SYSTEM_USER="${UNIFIED_SYSTEM_USER:-unified}"
SYSTEM_GROUP="${UNIFIED_SYSTEM_GROUP:-unified}"
DATA_DIR="${UNIFIED_DATA_DIR:-/var/lib/unified}"
BACKUP_DIR="${UNIFIED_BACKUP_DIR:-/var/backups/unified}"
WORK_DIR="${UNIFIED_WORK_DIR:-$DATA_DIR}"
ROTATE_SCRIPT_SOURCE="${UNIFIED_ROTATE_SCRIPT_SOURCE:-$ROOT_DIR/scripts/ops/rotate_backups.sh}"
ROTATE_SCRIPT_PATH="${UNIFIED_ROTATE_SCRIPT_PATH:-/usr/local/libexec/unified/rotate_backups.sh}"
SERVICE_PATH="${UNIFIED_BACKUP_SERVICE_PATH:-/etc/systemd/system/${SERVICE_NAME}.service}"
TIMER_PATH="${UNIFIED_BACKUP_TIMER_PATH:-/etc/systemd/system/${SERVICE_NAME}.timer}"
ON_CALENDAR="${UNIFIED_BACKUP_ON_CALENDAR:-daily}"
RANDOM_DELAY="${UNIFIED_BACKUP_RANDOM_DELAY:-15m}"
RETENTION="${UNIFIED_BACKUP_RETENTION:-7}"
ENABLE_TIMER="${UNIFIED_ENABLE_BACKUP_TIMER:-1}"
START_TIMER="${UNIFIED_START_BACKUP_TIMER:-1}"
DRY_RUN="${UNIFIED_DRY_RUN:-0}"

SERVICE_TEMPLATE="$ROOT_DIR/deploy/systemd/unified-backup.service.tmpl"
TIMER_TEMPLATE="$ROOT_DIR/deploy/systemd/unified-backup.timer.tmpl"

usage() {
	cat <<'EOF'
Install a systemd timer that rotates UniFied datadir backups on Linux.

Usage:
  sudo ./scripts/ops/install_backup_rotation.sh [--dry-run] [--no-start]

Relevant environment variables:
  UNIFIED_BACKUP_SERVICE_NAME
  UNIFIED_SYSTEM_USER
  UNIFIED_SYSTEM_GROUP
  UNIFIED_DATA_DIR
  UNIFIED_BACKUP_DIR
  UNIFIED_WORK_DIR
  UNIFIED_ROTATE_SCRIPT_SOURCE
  UNIFIED_ROTATE_SCRIPT_PATH
  UNIFIED_BACKUP_SERVICE_PATH
  UNIFIED_BACKUP_TIMER_PATH
  UNIFIED_BACKUP_ON_CALENDAR
  UNIFIED_BACKUP_RANDOM_DELAY
  UNIFIED_BACKUP_RETENTION
  UNIFIED_ENABLE_BACKUP_TIMER
  UNIFIED_START_BACKUP_TIMER
EOF
}

log() {
	echo "[install-backup-rotation] $*"
}

run_cmd() {
	if [[ "$DRY_RUN" == "1" ]]; then
		printf '[dry-run] '
		printf '%q ' "$@"
		printf '\n'
		return 0
	fi
	"$@"
}

escape_replacement() {
	printf '%s' "$1" | sed -e 's/[\/&]/\\&/g'
}

render_template() {
	local input="$1"
	local output="$2"
	shift 2

	run_cmd install -d -m 0755 "$(dirname "$output")"
	if [[ "$DRY_RUN" == "1" ]]; then
		echo "[dry-run] render $input -> $output"
		return 0
	fi

	cp "$input" "$output"
	while [[ $# -gt 0 ]]; do
		local token="$1"
		local value="$2"
		shift 2
		sed -i.bak "s/{{${token}}}/$(escape_replacement "$value")/g" "$output"
	done
	rm -f "${output}.bak"
}

require_root() {
	if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
		echo "Run this installer as root." >&2
		exit 1
	fi
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

ensure_prerequisites() {
	require_command systemctl
	require_command install
	require_command sed
	require_command chown
	if [[ ! -f "$SERVICE_TEMPLATE" || ! -f "$TIMER_TEMPLATE" || ! -f "$ROTATE_SCRIPT_SOURCE" ]]; then
		echo "Missing backup rotation templates or source script under $ROOT_DIR" >&2
		exit 1
	fi
}

ensure_paths() {
	run_cmd install -d -m 0755 /usr/local/libexec/unified
	run_cmd install -d -m 0755 "$BACKUP_DIR"
	run_cmd install -d -m 0755 "$WORK_DIR"
	run_cmd chown "$SYSTEM_USER:$SYSTEM_GROUP" "$BACKUP_DIR"
	run_cmd chown "$SYSTEM_USER:$SYSTEM_GROUP" "$WORK_DIR"
}

install_rotate_script() {
	run_cmd install -m 0755 "$ROTATE_SCRIPT_SOURCE" "$ROTATE_SCRIPT_PATH"
}

install_units() {
	render_template "$SERVICE_TEMPLATE" "$SERVICE_PATH" \
		SYSTEM_USER "$SYSTEM_USER" \
		SYSTEM_GROUP "$SYSTEM_GROUP" \
		BACKUP_RETENTION "$RETENTION" \
		ROTATE_SCRIPT "$ROTATE_SCRIPT_PATH" \
		DATA_DIR "$DATA_DIR" \
		BACKUP_DIR "$BACKUP_DIR" \
		WORK_DIR "$WORK_DIR"

	render_template "$TIMER_TEMPLATE" "$TIMER_PATH" \
		ON_CALENDAR "$ON_CALENDAR" \
		RANDOM_DELAY "$RANDOM_DELAY"
}

enable_timer() {
	run_cmd systemctl daemon-reload
	if [[ "$ENABLE_TIMER" == "1" ]]; then
		run_cmd systemctl enable "$(basename "$TIMER_PATH")"
	fi
	if [[ "$START_TIMER" == "1" ]]; then
		run_cmd systemctl restart "$(basename "$TIMER_PATH")"
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--dry-run)
			DRY_RUN=1
			;;
		--no-start)
			START_TIMER=0
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "Unknown argument: $1" >&2
			usage
			exit 1
			;;
	esac
	shift
done

require_root
ensure_prerequisites
ensure_paths
install_rotate_script
install_units
enable_timer

log "installed backup rotation timer ${SERVICE_NAME}.timer"
log "backups will be written to ${BACKUP_DIR}"
