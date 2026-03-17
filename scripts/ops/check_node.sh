#!/usr/bin/env bash

set -euo pipefail

RPC_URL="${UFI_RPC_URL:-http://127.0.0.1:8545}"
BASE_URL="${RPC_URL%/rpc}"
if [[ "$BASE_URL" == "$RPC_URL" ]]; then
  RPC_ENDPOINT="$RPC_URL/rpc"
else
  RPC_ENDPOINT="$RPC_URL"
fi
HEALTH_ENDPOINT="${BASE_URL}/healthz"

require_command() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "Missing required dependency: $1" >&2
		exit 1
	}
}

require_command curl

health_json="$(curl -fsS "$HEALTH_ENDPOINT")"
block_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"ufi_getBlockByNumber","params":{"number":"latest"}}')"
contracts_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":2,"method":"ufi_listContracts","params":{}}')"
uns_code_json="$(curl -fsS -X POST "$RPC_ENDPOINT" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":3,"method":"eth_getCode","params":["0x102","latest"]}')"

echo "Health: $health_json"
echo "Latest block: $block_json"
echo "System contracts: $contracts_json"
echo "UNS code: $uns_code_json"
