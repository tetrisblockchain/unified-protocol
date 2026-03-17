#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVICE_NAME="${UNIFIED_SERVICE_NAME:-unified-seed-node}"
SYSTEM_USER="${UNIFIED_SYSTEM_USER:-unified}"
SYSTEM_GROUP="${UNIFIED_SYSTEM_GROUP:-unified}"
INSTALL_PREFIX="${UNIFIED_INSTALL_PREFIX:-/usr/local/bin}"
BINARY_PATH="${UNIFIED_BINARY_PATH:-$INSTALL_PREFIX/unified-node}"
CONFIG_DIR="${UNIFIED_CONFIG_DIR:-/etc/unified}"
ENV_FILE="${UNIFIED_ENV_FILE:-$CONFIG_DIR/${SERVICE_NAME}.env}"
UNIT_PATH="${UNIFIED_UNIT_PATH:-/etc/systemd/system/${SERVICE_NAME}.service}"
DATA_DIR="${UNIFIED_DATA_DIR:-/var/lib/unified}"
LOG_DIR="${UNIFIED_LOG_DIR:-/var/log/unified}"
WORK_DIR="${UNIFIED_WORK_DIR:-$DATA_DIR}"

UNIFIED_MINE_VALUE="${UNIFIED_MINE:-true}"
UNIFIED_RPC_HOST_VALUE="${UNIFIED_RPC_HOST:-127.0.0.1}"
UNIFIED_RPC_PORT_VALUE="${UNIFIED_RPC_PORT:-8545}"
UNIFIED_P2P_LISTEN_VALUE="${UNIFIED_P2P_LISTEN:-/ip4/0.0.0.0/tcp/4001}"
UNIFIED_BOOTNODES_VALUE="${UNIFIED_BOOTNODES:-}"
UNIFIED_GENESIS_ADDRESS_VALUE="${UNIFIED_GENESIS_ADDRESS:-UFI_MAINNET_GENESIS_REPLACE_ME}"
UNIFIED_OPERATOR_ADDRESS_VALUE="${UNIFIED_OPERATOR_ADDRESS:-UFI_MAINNET_OPERATOR_REPLACE_ME}"
UNIFIED_OPERATOR_ALIAS_VALUE="${UNIFIED_OPERATOR_ALIAS:-mainnet-seed-1}"
UNIFIED_OPERATOR_VOTING_POWER_VALUE="${UNIFIED_OPERATOR_VOTING_POWER:-5000}"
UNIFIED_CIRCULATING_SUPPLY_VALUE="${UNIFIED_CIRCULATING_SUPPLY:-1000000}"

START_SERVICE="${UNIFIED_START_SERVICE:-0}"
OVERWRITE_ENV="${UNIFIED_OVERWRITE_ENV:-0}"
DRY_RUN="${UNIFIED_DRY_RUN:-0}"

ENV_TEMPLATE="$ROOT_DIR/deploy/env/unified-seed-node.env.tmpl"
UNIT_TEMPLATE="$ROOT_DIR/deploy/systemd/unified-seed-node.service.tmpl"

usage() {
	cat <<'EOF'
Install the UniFied seed node as a systemd service on Linux.

Usage:
  sudo ./scripts/ops/install_seed_node.sh [--start] [--overwrite-env] [--dry-run]

Relevant environment variables:
  UNIFIED_SERVICE_NAME
  UNIFIED_SYSTEM_USER
  UNIFIED_SYSTEM_GROUP
  UNIFIED_INSTALL_PREFIX
  UNIFIED_BINARY_PATH
  UNIFIED_CONFIG_DIR
  UNIFIED_ENV_FILE
  UNIFIED_UNIT_PATH
  UNIFIED_DATA_DIR
  UNIFIED_LOG_DIR
  UNIFIED_WORK_DIR
  UNIFIED_MINE
  UNIFIED_RPC_HOST
  UNIFIED_RPC_PORT
  UNIFIED_P2P_LISTEN
  UNIFIED_BOOTNODES
  UNIFIED_GENESIS_ADDRESS
  UNIFIED_OPERATOR_ADDRESS
  UNIFIED_OPERATOR_ALIAS
  UNIFIED_OPERATOR_VOTING_POWER
  UNIFIED_CIRCULATING_SUPPLY

Notes:
  - RPC binds to 127.0.0.1 by default. Keep it private and tunnel or reverse-proxy it deliberately.
  - The generated env file will contain REPLACE_ME placeholders unless you pass the network values at install time.
  - Starting the service is blocked when placeholder values remain in the env file.
EOF
}

log() {
	echo "[install-seed-node] $*"
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

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

parse_go_version() {
	local raw
	raw="$(go env GOVERSION 2>/dev/null || true)"
	if [[ -z "$raw" ]]; then
		raw="$(go version | awk '{print $3}')"
	fi
	raw="${raw#go}"
	echo "$raw"
}

check_go_version() {
	local version major minor
	version="$(parse_go_version)"
	major="${version%%.*}"
	minor="${version#*.}"
	minor="${minor%%.*}"

	if [[ -z "$major" || -z "$minor" ]]; then
		return 1
	fi
	if (( major < 1 )); then
		return 1
	fi
	if (( major == 1 && minor < 25 )); then
		return 1
	fi
	return 0
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

ensure_group() {
	if getent group "$SYSTEM_GROUP" >/dev/null 2>&1; then
		return 0
	fi
	run_cmd groupadd --system "$SYSTEM_GROUP"
}

ensure_user() {
	if id -u "$SYSTEM_USER" >/dev/null 2>&1; then
		return 0
	fi

	local shell_path
	shell_path="$(command -v nologin || true)"
	if [[ -z "$shell_path" ]]; then
		shell_path="/bin/false"
	fi

	run_cmd useradd \
		--system \
		--home-dir "$DATA_DIR" \
		--shell "$shell_path" \
		--gid "$SYSTEM_GROUP" \
		"$SYSTEM_USER"
}

ensure_directories() {
	run_cmd install -d -m 0750 -o "$SYSTEM_USER" -g "$SYSTEM_GROUP" "$DATA_DIR"
	run_cmd install -d -m 0750 -o "$SYSTEM_USER" -g "$SYSTEM_GROUP" "$WORK_DIR"
	run_cmd install -d -m 0750 -o "$SYSTEM_USER" -g "$SYSTEM_GROUP" "$LOG_DIR"
	run_cmd install -d -m 0755 -o root -g root "$CONFIG_DIR"
}

build_binary() {
	local tempdir tempbin
	tempdir="$(mktemp -d)"
	tempbin="$tempdir/unified-node"
	trap 'rm -rf "$tempdir"' RETURN

	if [[ "$DRY_RUN" == "1" ]]; then
		echo "[dry-run] (cd $ROOT_DIR && go build -trimpath -o $tempbin ./cmd/unified-node)"
		echo "[dry-run] install -m 0755 $tempbin $BINARY_PATH"
		return 0
	fi

	(
		cd "$ROOT_DIR"
		go build -trimpath -o "$tempbin" ./cmd/unified-node
	)
	run_cmd install -d -m 0755 "$(dirname "$BINARY_PATH")"
	run_cmd install -m 0755 "$tempbin" "$BINARY_PATH"
}

write_env_file() {
	if [[ -f "$ENV_FILE" && "$OVERWRITE_ENV" != "1" ]]; then
		log "keeping existing env file $ENV_FILE"
		return 0
	fi

	render_template \
		"$ENV_TEMPLATE" \
		"$ENV_FILE" \
		UNIFIED_MINE "$UNIFIED_MINE_VALUE" \
		DATA_DIR "$DATA_DIR" \
		RPC_HOST "$UNIFIED_RPC_HOST_VALUE" \
		RPC_PORT "$UNIFIED_RPC_PORT_VALUE" \
		P2P_LISTEN "$UNIFIED_P2P_LISTEN_VALUE" \
		BOOTNODES "$UNIFIED_BOOTNODES_VALUE" \
		GENESIS_ADDRESS "$UNIFIED_GENESIS_ADDRESS_VALUE" \
		OPERATOR_ADDRESS "$UNIFIED_OPERATOR_ADDRESS_VALUE" \
		OPERATOR_ALIAS "$UNIFIED_OPERATOR_ALIAS_VALUE" \
		OPERATOR_VOTING_POWER "$UNIFIED_OPERATOR_VOTING_POWER_VALUE" \
		CIRCULATING_SUPPLY "$UNIFIED_CIRCULATING_SUPPLY_VALUE"
	if [[ "$DRY_RUN" != "1" ]]; then
		chown root:"$SYSTEM_GROUP" "$ENV_FILE"
		chmod 0640 "$ENV_FILE"
	fi
}

write_unit_file() {
	render_template \
		"$UNIT_TEMPLATE" \
		"$UNIT_PATH" \
		SYSTEM_USER "$SYSTEM_USER" \
		SYSTEM_GROUP "$SYSTEM_GROUP" \
		WORK_DIR "$WORK_DIR" \
		ENV_FILE "$ENV_FILE" \
		BINARY_PATH "$BINARY_PATH" \
		DATA_DIR "$DATA_DIR" \
		LOG_DIR "$LOG_DIR"
	if [[ "$DRY_RUN" != "1" ]]; then
		chown root:root "$UNIT_PATH"
		chmod 0644 "$UNIT_PATH"
	fi
}

validate_startup_inputs() {
	if [[ ! -f "$ENV_FILE" ]]; then
		echo "Env file not found: $ENV_FILE" >&2
		exit 1
	fi
	if grep -q 'REPLACE_ME' "$ENV_FILE"; then
		echo "Refusing to start $SERVICE_NAME with placeholder main-network values in $ENV_FILE" >&2
		exit 1
	fi
}

reload_and_enable() {
	run_cmd systemctl daemon-reload
	run_cmd systemctl enable "$SERVICE_NAME"
}

start_service() {
	validate_startup_inputs
	run_cmd systemctl restart "$SERVICE_NAME"
	run_cmd systemctl --no-pager --full status "$SERVICE_NAME"
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--start)
		START_SERVICE=1
		shift
		;;
	--overwrite-env)
		OVERWRITE_ENV=1
		shift
		;;
	--dry-run)
		DRY_RUN=1
		shift
		;;
	-h|--help)
		usage
		exit 0
		;;
	*)
		echo "Unknown argument: $1" >&2
		usage >&2
		exit 1
		;;
	esac
done

if [[ "$(uname -s)" != "Linux" ]]; then
	echo "This installer only supports Linux/systemd hosts." >&2
	exit 1
fi

if [[ "$DRY_RUN" != "1" && "${EUID}" -ne 0 ]]; then
	echo "Run this installer as root or through sudo." >&2
	exit 1
fi

require_command go
require_command systemctl
require_command install
require_command sed
require_command cp
require_command awk
require_command useradd
require_command groupadd
require_command getent
require_command id

if ! check_go_version; then
	echo "Go 1.25+ is required. Found $(parse_go_version)." >&2
	exit 1
fi

if [[ ! -f "$ENV_TEMPLATE" || ! -f "$UNIT_TEMPLATE" ]]; then
	echo "Missing installer templates under $ROOT_DIR/deploy" >&2
	exit 1
fi

log "building unified-node from $ROOT_DIR"
build_binary
log "ensuring system user and directories"
ensure_group
ensure_user
ensure_directories
log "writing $ENV_FILE"
write_env_file
log "writing $UNIT_PATH"
write_unit_file
log "reloading systemd"
reload_and_enable

if [[ "$START_SERVICE" == "1" ]]; then
	log "starting $SERVICE_NAME"
	start_service
else
	log "installation complete"
fi

cat <<EOF

Seed node install summary:
  Service:      $SERVICE_NAME
  Binary:       $BINARY_PATH
  Env file:     $ENV_FILE
  Unit file:    $UNIT_PATH
  Data dir:     $DATA_DIR
  Log dir:      $LOG_DIR

Next steps:
  1. Edit $ENV_FILE and replace any REPLACE_ME values with the shared network settings.
  2. Review bootnodes and operator address before joining a shared network.
  3. Start the service with:
       sudo systemctl restart $SERVICE_NAME
  4. Inspect logs with:
       sudo journalctl -u $SERVICE_NAME -f

RPC remains bound to $UNIFIED_RPC_HOST_VALUE:$UNIFIED_RPC_PORT_VALUE by default.
Keep RPC private unless you deliberately front it with access controls.
EOF
