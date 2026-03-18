#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ROOT_DIR="${UNIFIED_SOURCE_ROOT:-$DEFAULT_ROOT_DIR}"

LABEL="${UNIFIED_LAUNCHD_LABEL:-io.unified.seed-node}"
INSTALL_PREFIX="${UNIFIED_INSTALL_PREFIX:-/usr/local/bin}"
BINARY_PATH="${UNIFIED_BINARY_PATH:-$INSTALL_PREFIX/unified-node}"
CONFIG_DIR="${UNIFIED_CONFIG_DIR:-/usr/local/etc/unified}"
ENV_FILE="${UNIFIED_ENV_FILE:-$CONFIG_DIR/unified-seed-node.env}"
NETWORK_CONFIG_PATH="${UNIFIED_NETWORK_CONFIG:-$CONFIG_DIR/unified-network.json}"
NETWORK_CONFIG_SOURCE="${UNIFIED_NETWORK_CONFIG_SOURCE:-}"
SUPPORT_DIR="${UNIFIED_SUPPORT_DIR:-/usr/local/libexec/unified}"
WRAPPER_PATH="${UNIFIED_WRAPPER_PATH:-$SUPPORT_DIR/unified-seed-node-launch.sh}"
PLIST_PATH="${UNIFIED_PLIST_PATH:-/Library/LaunchDaemons/${LABEL}.plist}"
DATA_DIR="${UNIFIED_DATA_DIR:-/usr/local/var/lib/unified}"
LOG_DIR="${UNIFIED_LOG_DIR:-/usr/local/var/log/unified}"
WORK_DIR="${UNIFIED_WORK_DIR:-$DATA_DIR}"
STDOUT_PATH="${UNIFIED_STDOUT_PATH:-$LOG_DIR/unified-seed-node.out.log}"
STDERR_PATH="${UNIFIED_STDERR_PATH:-$LOG_DIR/unified-seed-node.err.log}"

UNIFIED_MINE_VALUE="${UNIFIED_MINE:-true}"
UNIFIED_NETWORK_NAME_VALUE="${UNIFIED_NETWORK_NAME:-unified-mainnet}"
UNIFIED_CHAIN_ID_VALUE="${UNIFIED_CHAIN_ID:-333}"
UNIFIED_RPC_HOST_VALUE="${UNIFIED_RPC_HOST:-127.0.0.1}"
UNIFIED_RPC_PORT_VALUE="${UNIFIED_RPC_PORT:-8545}"
UNIFIED_P2P_LISTEN_VALUE="${UNIFIED_P2P_LISTEN:-/ip4/0.0.0.0/tcp/4001}"
UNIFIED_BOOTNODES_VALUE="${UNIFIED_BOOTNODES:-}"
UNIFIED_GENESIS_ADDRESS_VALUE="${UNIFIED_GENESIS_ADDRESS:-UFI_MAINNET_GENESIS_REPLACE_ME}"
UNIFIED_ARCHITECT_ADDRESS_VALUE="${UNIFIED_ARCHITECT_ADDRESS:-UFI_MAINNET_ARCHITECT_REPLACE_ME}"
UNIFIED_OPERATOR_ADDRESS_VALUE="${UNIFIED_OPERATOR_ADDRESS:-UFI_MAINNET_OPERATOR_REPLACE_ME}"
UNIFIED_OPERATOR_ALIAS_VALUE="${UNIFIED_OPERATOR_ALIAS:-mainnet-seed-1}"
UNIFIED_OPERATOR_VOTING_POWER_VALUE="${UNIFIED_OPERATOR_VOTING_POWER:-5000}"
UNIFIED_CIRCULATING_SUPPLY_VALUE="${UNIFIED_CIRCULATING_SUPPLY:-1000000}"

START_SERVICE="${UNIFIED_START_SERVICE:-0}"
OVERWRITE_ENV="${UNIFIED_OVERWRITE_ENV:-0}"
DRY_RUN="${UNIFIED_DRY_RUN:-0}"

BUILD_PACKAGE="${UNIFIED_BUILD_PACKAGE:-}"
ENV_TEMPLATE=""
PLIST_TEMPLATE=""
NETWORK_TEMPLATE=""

usage() {
	cat <<'EOF'
Install the UniFied seed node as a launchd service on macOS.

Usage:
  sudo ./scripts/ops/install_seed_node_macos.sh [--start] [--overwrite-env] [--dry-run]

Relevant environment variables:
  UNIFIED_SOURCE_ROOT
  UNIFIED_BUILD_PACKAGE
  UNIFIED_LAUNCHD_LABEL
  UNIFIED_INSTALL_PREFIX
  UNIFIED_BINARY_PATH
  UNIFIED_CONFIG_DIR
  UNIFIED_ENV_FILE
  UNIFIED_NETWORK_CONFIG
  UNIFIED_NETWORK_CONFIG_SOURCE
  UNIFIED_SUPPORT_DIR
  UNIFIED_WRAPPER_PATH
  UNIFIED_PLIST_PATH
  UNIFIED_DATA_DIR
  UNIFIED_LOG_DIR
  UNIFIED_WORK_DIR
  UNIFIED_STDOUT_PATH
  UNIFIED_STDERR_PATH
  UNIFIED_MINE
  UNIFIED_NETWORK_NAME
  UNIFIED_CHAIN_ID
  UNIFIED_RPC_HOST
  UNIFIED_RPC_PORT
  UNIFIED_P2P_LISTEN
  UNIFIED_BOOTNODES
  UNIFIED_GENESIS_ADDRESS
  UNIFIED_ARCHITECT_ADDRESS
  UNIFIED_OPERATOR_ADDRESS
  UNIFIED_OPERATOR_ALIAS
  UNIFIED_OPERATOR_VOTING_POWER
  UNIFIED_CIRCULATING_SUPPLY

Notes:
  - This installer writes a LaunchDaemon, so run it as root.
  - RPC binds to 127.0.0.1 by default. Keep it private unless you intentionally expose it.
  - Shared network settings are written to a JSON manifest referenced by the env file.
  - Set UNIFIED_NETWORK_CONFIG_SOURCE to copy one exact pinned manifest instead of rendering from env values.
  - Starting the service is blocked when placeholder values remain in either file.
EOF
}

log() {
	echo "[install-seed-node-macos] $*"
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
		if [[ "$1" == "go" ]]; then
			echo "Missing required dependency: go" >&2
			echo "Install Go first, for example with Homebrew:" >&2
			echo "  brew install go" >&2
			exit 1
		fi
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

configure_source_layout() {
	local candidate template_base

	if [[ -n "$BUILD_PACKAGE" ]]; then
		if [[ ! -d "${ROOT_DIR}/${BUILD_PACKAGE#./}" ]]; then
			echo "Configured build package not found under ${ROOT_DIR}: ${BUILD_PACKAGE}" >&2
			exit 1
		fi
	else
		for candidate in "./cmd/unified-node" "./unified-protocol/cmd/unified-node"; do
			if [[ -d "${ROOT_DIR}/${candidate#./}" ]]; then
				BUILD_PACKAGE="$candidate"
				break
			fi
		done
	fi

	if [[ -z "$BUILD_PACKAGE" ]]; then
		echo "Could not find a node source package under ${ROOT_DIR}." >&2
		echo "Set UNIFIED_SOURCE_ROOT or UNIFIED_BUILD_PACKAGE if your checkout is elsewhere." >&2
		exit 1
	fi

	for template_base in "${ROOT_DIR}/deploy" "${DEFAULT_ROOT_DIR}/deploy"; do
		if [[ -f "${template_base}/env/unified-seed-node.env.tmpl" && -f "${template_base}/launchd/io.unified.seed-node.plist.tmpl" && -f "${template_base}/network/unified-network.json.tmpl" ]]; then
			ENV_TEMPLATE="${template_base}/env/unified-seed-node.env.tmpl"
			PLIST_TEMPLATE="${template_base}/launchd/io.unified.seed-node.plist.tmpl"
			NETWORK_TEMPLATE="${template_base}/network/unified-network.json.tmpl"
			break
		fi
	done

	if [[ -z "$ENV_TEMPLATE" || -z "$PLIST_TEMPLATE" || -z "$NETWORK_TEMPLATE" ]]; then
		echo "Missing launchd installer templates under ${ROOT_DIR}/deploy or ${DEFAULT_ROOT_DIR}/deploy" >&2
		exit 1
	fi
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

json_escape() {
	local value="$1"
	value="${value//\\/\\\\}"
	value="${value//\"/\\\"}"
	value="${value//$'\n'/}"
	printf '%s' "$value"
}

bootnodes_json() {
	local raw="$1"
	local first=1
	local part cleaned

	printf '['
	IFS=',' read -r -a parts <<<"$raw"
	for part in "${parts[@]}"; do
		cleaned="$(printf '%s' "$part" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
		if [[ -z "$cleaned" ]]; then
			continue
		fi
		if [[ $first -eq 0 ]]; then
			printf ', '
		fi
		printf '"%s"' "$(json_escape "$cleaned")"
		first=0
	done
	printf ']'
}

ensure_directories() {
	run_cmd install -d -m 0755 "$CONFIG_DIR"
	run_cmd install -d -m 0755 "$SUPPORT_DIR"
	run_cmd install -d -m 0755 "$DATA_DIR"
	run_cmd install -d -m 0755 "$WORK_DIR"
	run_cmd install -d -m 0755 "$LOG_DIR"
}

build_binary() {
	local tempdir tempbin
	tempdir="$(mktemp -d)"
	tempbin="$tempdir/unified-node"
	trap 'rm -rf "$tempdir"' RETURN

	if [[ "$DRY_RUN" == "1" ]]; then
		echo "[dry-run] (cd $ROOT_DIR && go build -trimpath -o $tempbin $BUILD_PACKAGE)"
		echo "[dry-run] install -m 0755 $tempbin $BINARY_PATH"
		return 0
	fi

	(
		cd "$ROOT_DIR"
		go build -trimpath -o "$tempbin" "$BUILD_PACKAGE"
	)
	run_cmd install -d -m 0755 "$(dirname "$BINARY_PATH")"
	run_cmd install -m 0755 "$tempbin" "$BINARY_PATH"
}

write_network_config_file() {
	local bootnodes_json_value
	if [[ -f "$NETWORK_CONFIG_PATH" && "$OVERWRITE_ENV" != "1" ]]; then
		log "keeping existing network config $NETWORK_CONFIG_PATH"
		return 0
	fi
	if [[ -n "$NETWORK_CONFIG_SOURCE" ]]; then
		if [[ ! -f "$NETWORK_CONFIG_SOURCE" ]]; then
			echo "Pinned network config source not found: $NETWORK_CONFIG_SOURCE" >&2
			exit 1
		fi
		run_cmd install -d -m 0755 "$(dirname "$NETWORK_CONFIG_PATH")"
		if [[ "$DRY_RUN" == "1" ]]; then
			echo "[dry-run] copy $NETWORK_CONFIG_SOURCE -> $NETWORK_CONFIG_PATH"
		else
			cp "$NETWORK_CONFIG_SOURCE" "$NETWORK_CONFIG_PATH"
			chmod 0644 "$NETWORK_CONFIG_PATH"
		fi
		return 0
	fi
	bootnodes_json_value="$(bootnodes_json "$UNIFIED_BOOTNODES_VALUE")"

	render_template \
		"$NETWORK_TEMPLATE" \
		"$NETWORK_CONFIG_PATH" \
		NETWORK_NAME "$UNIFIED_NETWORK_NAME_VALUE" \
		CHAIN_ID "$UNIFIED_CHAIN_ID_VALUE" \
		GENESIS_ADDRESS "$UNIFIED_GENESIS_ADDRESS_VALUE" \
		ARCHITECT_ADDRESS "$UNIFIED_ARCHITECT_ADDRESS_VALUE" \
		CIRCULATING_SUPPLY "$UNIFIED_CIRCULATING_SUPPLY_VALUE" \
		BOOTNODES_JSON "$bootnodes_json_value"
	if [[ "$DRY_RUN" != "1" ]]; then
		chmod 0644 "$NETWORK_CONFIG_PATH"
	fi
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
		NETWORK_CONFIG "$NETWORK_CONFIG_PATH" \
		OPERATOR_ADDRESS "$UNIFIED_OPERATOR_ADDRESS_VALUE" \
		OPERATOR_ALIAS "$UNIFIED_OPERATOR_ALIAS_VALUE" \
		OPERATOR_VOTING_POWER "$UNIFIED_OPERATOR_VOTING_POWER_VALUE"
	if [[ "$DRY_RUN" != "1" ]]; then
		chmod 0644 "$ENV_FILE"
	fi
}

write_wrapper() {
	run_cmd install -d -m 0755 "$(dirname "$WRAPPER_PATH")"
	if [[ "$DRY_RUN" == "1" ]]; then
		echo "[dry-run] write $WRAPPER_PATH"
		return 0
	fi

	cat >"$WRAPPER_PATH" <<EOF
#!/usr/bin/env bash
set -euo pipefail

source "$ENV_FILE"

args=(
  --network-config "\${UNIFIED_NETWORK_CONFIG}"
  --datadir "\${UNIFIED_DATADIR}"
  --rpchost "\${UNIFIED_RPC_HOST}"
  --rpcport "\${UNIFIED_RPC_PORT}"
  --p2p-listen "\${UNIFIED_P2P_LISTEN}"
  --operator "\${UNIFIED_OPERATOR_ADDRESS}"
  --operator-alias "\${UNIFIED_OPERATOR_ALIAS}"
  --operator-voting-power "\${UNIFIED_OPERATOR_VOTING_POWER}"
)

case "\${UNIFIED_MINE:-false}" in
  1|true|TRUE|yes|YES|on|ON)
    args=(--mine "\${args[@]}")
    ;;
esac

exec "$BINARY_PATH" "\${args[@]}"
EOF
	chmod 0755 "$WRAPPER_PATH"
}

write_plist() {
	render_template \
		"$PLIST_TEMPLATE" \
		"$PLIST_PATH" \
		LABEL "$LABEL" \
		WRAPPER_PATH "$WRAPPER_PATH" \
		WORK_DIR "$WORK_DIR" \
		STDOUT_PATH "$STDOUT_PATH" \
		STDERR_PATH "$STDERR_PATH"
	if [[ "$DRY_RUN" != "1" ]]; then
		chmod 0644 "$PLIST_PATH"
		plutil -lint "$PLIST_PATH" >/dev/null
	fi
}

validate_startup_inputs() {
	if [[ ! -f "$ENV_FILE" ]]; then
		echo "Env file not found: $ENV_FILE" >&2
		exit 1
	fi
	if [[ ! -f "$NETWORK_CONFIG_PATH" ]]; then
		echo "Network config not found: $NETWORK_CONFIG_PATH" >&2
		exit 1
	fi
	if grep -q 'REPLACE_ME' "$ENV_FILE" || grep -q 'REPLACE_ME' "$NETWORK_CONFIG_PATH"; then
		echo "Refusing to start $LABEL with placeholder main-network values in $ENV_FILE or $NETWORK_CONFIG_PATH" >&2
		exit 1
	fi
}

reload_service() {
	if [[ "$DRY_RUN" == "1" ]]; then
		run_cmd launchctl bootout system "$PLIST_PATH"
		run_cmd launchctl bootstrap system "$PLIST_PATH"
		run_cmd launchctl enable "system/$LABEL"
		return 0
	fi
	launchctl bootout system "$PLIST_PATH" >/dev/null 2>&1 || true
	launchctl bootstrap system "$PLIST_PATH"
	launchctl enable "system/$LABEL"
}

start_service() {
	validate_startup_inputs
	reload_service
	run_cmd launchctl kickstart -k "system/$LABEL"
	run_cmd launchctl print "system/$LABEL"
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

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "This installer only supports macOS." >&2
	exit 1
fi

if [[ "$DRY_RUN" != "1" && "${EUID}" -ne 0 ]]; then
	echo "Run this installer as root or through sudo." >&2
	exit 1
fi

require_command go
require_command launchctl
require_command install
require_command sed
require_command cp
require_command awk
require_command plutil

if ! check_go_version; then
	echo "Go 1.25+ is required. Found $(parse_go_version)." >&2
	exit 1
fi

configure_source_layout

log "building unified-node from $ROOT_DIR using $BUILD_PACKAGE"
build_binary
log "ensuring directories"
ensure_directories
log "writing $NETWORK_CONFIG_PATH"
write_network_config_file
log "writing $ENV_FILE"
write_env_file
log "writing $WRAPPER_PATH"
write_wrapper
log "writing $PLIST_PATH"
write_plist

if [[ "$START_SERVICE" == "1" ]]; then
	log "starting $LABEL"
	start_service
else
	log "installation complete"
fi

cat <<EOF

Seed node install summary:
  Label:        $LABEL
  Binary:       $BINARY_PATH
  Network cfg:  $NETWORK_CONFIG_PATH
  Env file:     $ENV_FILE
  Wrapper:      $WRAPPER_PATH
  Plist:        $PLIST_PATH
  Data dir:     $DATA_DIR
  Log dir:      $LOG_DIR

Next steps:
  1. Edit $NETWORK_CONFIG_PATH and $ENV_FILE if any REPLACE_ME values remain.
  2. Start the service with:
       sudo launchctl bootstrap system $PLIST_PATH
       sudo launchctl kickstart -k system/$LABEL
  3. Inspect status:
       sudo launchctl print system/$LABEL
  4. Inspect logs:
       tail -f "$STDOUT_PATH" "$STDERR_PATH"

RPC remains bound to $UNIFIED_RPC_HOST_VALUE:$UNIFIED_RPC_PORT_VALUE by default.
Keep RPC private unless you deliberately front it with access controls.
EOF
