# UniFied

UniFied is an experimental Layer 1 prototype for Proof of Useful Work search indexing. The current workspace now includes a persistent blockchain state engine, a 15-second mining loop, JSON-RPC, governance-aware crawl priority rules, and libp2p GossipSub block propagation.

## Current Components

- `cmd/unified-node`: persistent node daemon with BadgerDB-backed chain state, JSON-RPC, governance endpoints, and libp2p networking.
- `cmd/unified-cli`: governance CLI for listing proposals and casting votes.
- `core/blockchain.go`: ledger persistence, transaction/state transitions, search index storage, and architect fee enforcement.
- `core/engine.go`: mempools plus the PoUW mining loop.
- `api/rpc_server.go`: JSON-RPC methods for balances, transfers, blocks, search task submission, and local search-index reads.
- `contracts/UNS.sol`: UNS registry contract that prices names from the `0x101` search precompile mention frequency.

## Launch Readiness

- `Improved`: remote blocks now pass local PoUW validator-quorum checks before import, peers can sync missing blocks over libp2p, governance state survives node restarts, and the node persists side branches with cumulative-work-based reorgs onto the heavier canonical chain.
- `Still missing`: peer/mempool abuse controls are still minimal, and the cumulative-work model is still a prototype weighting scheme rather than a hardened adversarial mainnet metric.
- `Operational requirement`: all nodes on the same network must share the same genesis configuration, especially `--genesis-address` and circulating supply, or they will form incompatible chains.

## Run The Node

Prerequisite: Go 1.25+

```bash
go run ./cmd/unified-node
```

Common flags:

```bash
go run ./cmd/unified-node \
  --datadir ./data \
  --rpcport 8545 \
  --mine \
  --bootnodes /ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  --genesis-address UFI_SHARED_GENESIS_FUNDER \
  --operator UFI_LOCAL_OPERATOR \
  --operator-alias local-operator \
  --operator-voting-power 5000 \
  --circulating-supply 1000000
```

The daemon mounts governance REST endpoints and JSON-RPC on the same HTTP server. JSON-RPC is available on `/rpc`, while `/healthz`, `/governance/*`, `/chain/*`, and `/consensus/quote` remain available for the existing CLI and governance tooling.

For multi-node operation, every peer must start with the same `--genesis-address` and `--circulating-supply` values so the genesis block hash and state root match across the network.

## JSON-RPC Examples

Get the latest block:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ufi_getBlockByNumber","params":{"number":"latest"}}'
```

Get a balance:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"ufi_getBalance","params":{"address":"UFI_LOCAL_OPERATOR"}}'
```

Read indexed crawl data:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"ufi_getSearchData","params":{"url":"https://example.edu","term":"search"}}'
```

## Genesis Script

The genesis bootstrap script signs the Architect UNS registration locally, broadcasts it through `ufi_sendRawTransaction`, waits for block `#1`, and then submits the first crawl seed task.

Run the node with the Architect address used as the shared genesis address, then execute:

```bash
go run ./cmd/unified-node --mine --operator <architect-ufi-address> --genesis-address <architect-ufi-address>

UFI_ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
UFI_RPC_URL=http://127.0.0.1:8545 \
go run ./scripts/genesis_tx.go
```

Optional overrides:

```bash
UFI_ARCHITECT_ADDRESS=<expected-ufi-address> \
UFI_GENESIS_URL=https://unified.network/genesis \
UFI_GENESIS_QUERY="UniFied genesis seed" \
go run ./scripts/genesis_tx.go
```

## Governance Flow

Create a proposal:

```bash
curl -X POST http://127.0.0.1:8545/governance/proposals \
  -H 'Content-Type: application/json' \
  -d '{
    "title":"UGP-001 Prioritize EDU",
    "targetComponent":"pouw.go",
    "logicExtension":"Prioritize .edu crawl rewards",
    "sector":".edu",
    "multiplierBps":15000,
    "stake":"1000"
  }'
```

Vote with the local operator:

```bash
go run ./cmd/unified-cli vote --proposal 1 --choice Yes
```

Advance local governance height and finalize:

```bash
curl -X POST http://127.0.0.1:8545/chain/advance \
  -H 'Content-Type: application/json' \
  -d '{"blocks":40321}'

curl -X POST http://127.0.0.1:8545/governance/finalize \
  -H 'Content-Type: application/json' \
  -d '{"proposalId":1}'
```

Inspect the active multiplier rules:

```bash
curl http://127.0.0.1:8545/governance/rules
```

Quote a governance-adjusted crawl bounty:

```bash
curl -X POST http://127.0.0.1:8545/consensus/quote \
  -H 'Content-Type: application/json' \
  -d '{
    "query":"distributed search",
    "url":"https://mit.edu",
    "baseBounty":"100",
    "difficulty":20,
    "dataVolumeBytes":10
  }'
```
