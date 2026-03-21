#!/usr/bin/env bash

set -euo pipefail

RPC_URL="${UFI_RPC_URL:-http://127.0.0.1:3337}"
BASE_URL="${RPC_URL%/rpc}"
if [[ "$BASE_URL" == "$RPC_URL" ]]; then
  RPC_ENDPOINT="$RPC_URL/rpc"
else
  RPC_ENDPOINT="$RPC_URL"
fi
HEALTH_ENDPOINT="${BASE_URL}/healthz"
READY_ENDPOINT="${BASE_URL}/readyz"

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

require_command curl

health_json="$(curl -fsS "$HEALTH_ENDPOINT")"
ready_json="$(curl -fsS "$READY_ENDPOINT" || curl -sS "$READY_ENDPOINT")"
network_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":0,"method":"ufi_getNetworkConfig","params":{}}')"
chain_id_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":4,"method":"eth_chainId","params":[]}')"
block_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"ufi_getBlockByNumber","params":{"number":"latest"}}')"
contracts_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":2,"method":"ufi_listContracts","params":{}}')"
uns_code_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":3,"method":"eth_getCode","params":["0x102","latest"]}')"
p2p_json="$(curl -fsS "${BASE_URL}/p2p/peers" || curl -sS "${BASE_URL}/p2p/peers")"

echo "Health: $health_json"
echo "Ready: $ready_json"
echo "Network: $network_json"
echo "Chain ID: $chain_id_json"
echo "Latest block: $block_json"
echo "System contracts: $contracts_json"
echo "UNS code: $uns_code_json"
echo "P2P: $p2p_json"
