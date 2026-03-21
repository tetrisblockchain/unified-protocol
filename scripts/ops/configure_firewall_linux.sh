#!/usr/bin/env bash

set -euo pipefail

P2P_PORT="${UNIFIED_P2P_PORT:-4001}"
SSH_PORT="${UNIFIED_SSH_PORT:-22}"
ALLOW_RPC_PUBLIC="${UNIFIED_ALLOW_RPC_PUBLIC:-0}"
RPC_PORT="${UNIFIED_RPC_PORT:-3337}"
DRY_RUN="${UNIFIED_DRY_RUN:-0}"

usage() {
	cat <<'EOF'
Configure a minimal Linux firewall profile for a UniFied seed node using ufw.

Usage:
  sudo ./scripts/ops/configure_firewall_linux.sh [--dry-run] [--allow-rpc-public]

Behavior:
  - allows inbound SSH on UNIFIED_SSH_PORT (default 22)
  - allows inbound libp2p on UNIFIED_P2P_PORT (default 4001)
  - denies inbound traffic by default
  - keeps RPC private unless --allow-rpc-public is passed
EOF
}

log() {
	echo "[configure-firewall] $*"
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

require_root() {
	if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
		echo "Run this firewall helper as root." >&2
		exit 1
	fi
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

validate_port() {
	local port="$1"
	if ! [[ "$port" =~ ^[0-9]+$ ]] || (( port < 1 || port > 65535 )); then
		echo "Invalid port: $port" >&2
		exit 1
	fi
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--dry-run)
			DRY_RUN=1
			;;
		--allow-rpc-public)
			ALLOW_RPC_PUBLIC=1
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
require_command ufw
validate_port "$P2P_PORT"
validate_port "$SSH_PORT"
validate_port "$RPC_PORT"

run_cmd ufw --force default deny incoming
run_cmd ufw --force default allow outgoing
run_cmd ufw allow "${SSH_PORT}/tcp" comment 'UniFied SSH'
run_cmd ufw allow "${P2P_PORT}/tcp" comment 'UniFied libp2p'

if [[ "$ALLOW_RPC_PUBLIC" == "1" ]]; then
	run_cmd ufw allow "${RPC_PORT}/tcp" comment 'UniFied RPC'
	log "RPC port ${RPC_PORT} exposed publicly"
else
	log "RPC port ${RPC_PORT} left private; keep RPCHOST on 127.0.0.1"
fi

run_cmd ufw --force enable
run_cmd ufw status numbered
