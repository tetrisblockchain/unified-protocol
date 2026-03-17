# UniFied Runbook

This runbook covers the current prototype daemon in this repository. It assumes a shared genesis configuration, local BadgerDB state, JSON-RPC on `/rpc`, and the optional PoUW mining loop.

## Prerequisites

- Go 1.25 or newer
- `make`
- `curl`
- `npx` if you want to compile Solidity artifacts locally

Initialize the workspace:

```bash
./setup.sh
```

## Single-Node Devnet

Start a local node without mining:

```bash
make run-node \
  DATADIR=./data/devnet-a \
  OPERATOR=UFI_LOCAL_OPERATOR \
  GENESIS_ADDRESS=UFI_LOCAL_OPERATOR
```

Start a local node with mining enabled:

```bash
make run-mine \
  DATADIR=./data/devnet-a \
  OPERATOR=UFI_LOCAL_OPERATOR \
  GENESIS_ADDRESS=UFI_LOCAL_OPERATOR
```

Health and latest-block checks:

```bash
make smoke-health
make smoke-rpc
```

## Multi-Node Bootstrap

Every node on the same network must share:

- `GENESIS_ADDRESS`
- `CIRCULATING_SUPPLY`

Start node A and copy one of the printed libp2p listen addresses:

```bash
make run-mine \
  DATADIR=./data/devnet-a \
  OPERATOR=UFI_NODE_A \
  GENESIS_ADDRESS=UFI_SHARED_GENESIS \
  CIRCULATING_SUPPLY=1000000
```

Start node B with node A as a bootnode:

```bash
make run-node \
  DATADIR=./data/devnet-b \
  OPERATOR=UFI_NODE_B \
  GENESIS_ADDRESS=UFI_SHARED_GENESIS \
  CIRCULATING_SUPPLY=1000000 \
  BOOTNODES=/ip4/127.0.0.1/tcp/4001/p2p/12D3KooW...
```

## Genesis Bootstrap

Fastest path:

```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make bootstrap-architect \
  DATADIR=./data/genesis-architect
```

That script derives the Architect address from the supplied key, starts the node in the background with mining enabled, waits for the local health check, runs the genesis registration and seed task, and leaves the node running. Node logs are written to `./logs/architect-bootstrap-*.log`.

Manual path:

1. Start the node with mining enabled and the architect address as both `OPERATOR` and `GENESIS_ADDRESS`.
2. Export the architect private key.
3. Run the genesis script to register the `Architect` UNS name and submit the seed crawl task.

```bash
make run-mine \
  DATADIR=./data/genesis \
  OPERATOR=<architect-ufi-address> \
  GENESIS_ADDRESS=<architect-ufi-address>
```

In a second shell:

```bash
make genesis \
  RPCPORT=8545 \
  ARCHITECT_KEY=<hex-or-base64-ed25519-key>
```

Optional seed overrides:

```bash
make genesis \
  RPCPORT=8545 \
  ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
  GENESIS_URL=https://unified.network/genesis \
  GENESIS_QUERY="UniFied genesis seed"
```

## Build And Test

Run the current Go test suite:

```bash
make test
```

Build the CLI and node binaries:

```bash
make build
```

Compile the UNS Solidity artifact:

```bash
make solc-uns
```

## Contract Introspection

The node exposes the protocol system contracts as first-class introspection targets.

List them:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ufi_listContracts","params":{}}'
```

Fetch one contract record:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"ufi_getContract","params":{"address":"0x102"}}'
```

Inspect descriptor bytecode:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"eth_getCode","params":["0x102","latest"]}'
```

These system contracts are protocol-owned descriptors backed by native handlers, not a full general-purpose EVM deployment pipeline.

## Bulk URL Seeding

Prepare a file with one URL per line. Optional per-line query overrides are supported as `url,query`.

Example:

```text
https://example.com
https://openai.com,ai research
https://mit.edu
```

Run the seeder against the local node:

```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make seed-urls \
  URLS_FILE=./urls.txt \
  SEED_QUERY="initial web seed" \
  SEED_BASE_BOUNTY=1.0 \
  SEED_DIFFICULTY=8 \
  SEED_DATA_VOLUME_BYTES=1024
```

Operational notes:

- The sender account must have enough UFD for the full quoted bounty set.
- The script refuses to start if the sender already has pending transactions.
- One sender can keep at most `32` tasks in flight with the current node limits, so large seeds are submitted in waves while the miner drains the queue.

## Operations Toolkit

Health and surface check:

```bash
make check-node
```

Datadir backup:

```bash
make backup-datadir DATADIR=./data/local
```

Datadir restore:

```bash
make restore-datadir BACKUP_ARCHIVE=./data-local-20260317-120000.tar.gz RESTORE_TARGET=./data/restored
```

Build/test/backup rollout:

```bash
make rollout-node DATADIR=./data/local
```

For an automatic restart after build and backup:

```bash
UFI_RESTART=1 make rollout-node DATADIR=./data/local PID_FILE=./run/unified-node.pid
```

## Shutdown And Recovery

- Stop the daemon with `Ctrl+C` or a `SIGTERM` to allow the HTTP server and BadgerDB to shut down cleanly.
- Preserve the entire datadir when moving or backing up a node. The chain state, branch snapshots, and governance persistence depend on it.
- Restore by placing the datadir back in the same path or by pointing `DATADIR` to the restored directory.

## Upgrade Notes

- Keep nodes on the same binary version during testing. There is no formal network upgrade handshake yet.
- If you change genesis parameters, start from a fresh datadir. Old state will not match the new genesis root.
- Governance state persists locally, but proposal semantics are still prototype-grade and should not be treated as immutable production governance.

## Current Limits

- Native contracts `0x101` and `0x102` are node-implemented system contracts, not a full deployed EVM contract model.
- Peer reputation and adaptive abuse controls are still incomplete.
- The cumulative-work model is a prototype and should not yet be treated as adversarially hardened.
