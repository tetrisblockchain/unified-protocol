#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

RPC_HOST="${UFI_RPC_HOST:-127.0.0.1}"
RPC_PORT="${UFI_RPC_PORT:-8545}"
DATADIR="${UFI_DATADIR:-$ROOT_DIR/data/genesis-architect}"
NETWORK_CONFIG="${UFI_NETWORK_CONFIG:-}"
P2P_LISTEN="${UFI_P2P_LISTEN:-/ip4/0.0.0.0/tcp/0}"
BOOTNODES="${UFI_BOOTNODES:-}"
OPERATOR_ALIAS="${UFI_OPERATOR_ALIAS:-architect}"
OPERATOR_VOTING_POWER="${UFI_OPERATOR_VOTING_POWER:-5000}"
CIRCULATING_SUPPLY="${UFI_CIRCULATING_SUPPLY:-1000000}"
HEALTH_TIMEOUT_SECONDS="${UFI_HEALTH_TIMEOUT_SECONDS:-30}"

NODE_PID=""
NODE_LOG=""
KEEP_RUNNING=1

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

derive_architect_address() {
	local helper
	helper="$(mktemp "${TMPDIR:-/tmp}/ufi-architect-addr-XXXXXX.go")"
	cat >"$helper" <<'EOF'
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"unified/core/types"
)

func main() {
	raw := strings.TrimSpace(os.Getenv("UFI_ARCHITECT_KEY"))
	if raw == "" {
		panic("UFI_ARCHITECT_KEY is required")
	}

	decoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			panic("UFI_ARCHITECT_KEY must be hex or base64 encoded")
		}
	}

	var privateKey ed25519.PrivateKey
	switch len(decoded) {
	case ed25519.SeedSize:
		privateKey = ed25519.NewKeyFromSeed(decoded)
	case ed25519.PrivateKeySize:
		privateKey = ed25519.PrivateKey(decoded)
	default:
		panic("UFI_ARCHITECT_KEY must decode to a 32-byte seed or 64-byte private key")
	}

	address, err := types.NewAddressFromPubKey(privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		panic(err)
	}
	fmt.Print(address.String())
}
EOF

	local address
	address="$(cd "$ROOT_DIR" && go run "$helper")"
	rm -f "$helper"
	printf '%s' "$address"
}

cleanup_on_error() {
	local exit_code=$?
	if [[ $exit_code -ne 0 && -n "$NODE_PID" ]]; then
		echo "Bootstrap failed. Stopping background node $NODE_PID" >&2
		kill "$NODE_PID" >/dev/null 2>&1 || true
		wait "$NODE_PID" >/dev/null 2>&1 || true
	fi
	exit "$exit_code"
}

wait_for_health() {
	local deadline=$((SECONDS + HEALTH_TIMEOUT_SECONDS))
	local url="http://${RPC_HOST}:${RPC_PORT}/healthz"

	while (( SECONDS < deadline )); do
		if curl -fsS "$url" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done

	echo "Timed out waiting for node health endpoint at $url" >&2
	return 1
}

require_command go
require_command curl
require_command mktemp

if [[ -z "${UFI_ARCHITECT_KEY:-}" ]]; then
	echo "UFI_ARCHITECT_KEY is required" >&2
	exit 1
fi

trap cleanup_on_error EXIT

ARCH_ADDR="$(derive_architect_address)"
if [[ -n "${UFI_ARCHITECT_ADDRESS:-}" && "${UFI_ARCHITECT_ADDRESS}" != "$ARCH_ADDR" ]]; then
	echo "UFI_ARCHITECT_ADDRESS=${UFI_ARCHITECT_ADDRESS} does not match derived address ${ARCH_ADDR}" >&2
	exit 1
fi

export UFI_ARCHITECT_ADDRESS="$ARCH_ADDR"
export UFI_RPC_URL="http://${RPC_HOST}:${RPC_PORT}"

mkdir -p "$DATADIR" "$ROOT_DIR/logs"
NODE_LOG="$ROOT_DIR/logs/architect-bootstrap-$(date +%Y%m%d-%H%M%S).log"

echo "Architect address: $ARCH_ADDR"
echo "Datadir: $DATADIR"
echo "Node log: $NODE_LOG"

cd "$ROOT_DIR"
go run ./cmd/unified-node \
	--mine \
	--network-config "$NETWORK_CONFIG" \
	--datadir "$DATADIR" \
	--rpchost "$RPC_HOST" \
	--rpcport "$RPC_PORT" \
	--p2p-listen "$P2P_LISTEN" \
	--bootnodes "$BOOTNODES" \
	--genesis-address "$ARCH_ADDR" \
	--architect-address "$ARCH_ADDR" \
	--operator "$ARCH_ADDR" \
	--operator-alias "$OPERATOR_ALIAS" \
	--operator-voting-power "$OPERATOR_VOTING_POWER" \
	--circulating-supply "$CIRCULATING_SUPPLY" \
	>"$NODE_LOG" 2>&1 &
NODE_PID=$!

echo "Started unified-node in background with PID $NODE_PID"
wait_for_health
echo "Node is healthy. Running genesis transaction."

go run ./scripts/genesis_tx

echo "Architect bootstrap complete. Node is still running in the background."
echo "RPC endpoint: ${UFI_RPC_URL}"
echo "Node PID: $NODE_PID"
echo "Node log: $NODE_LOG"

trap - EXIT
