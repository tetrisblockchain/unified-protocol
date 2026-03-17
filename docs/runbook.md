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
