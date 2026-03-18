# UniFied

UniFied is an experimental Layer 1 prototype for Proof of Useful Work search indexing. The current workspace now includes a persistent blockchain state engine, a 15-second mining loop, JSON-RPC, governance-aware crawl priority rules, and libp2p GossipSub block propagation.

## Current Components

- `setup.sh` and `Makefile`: root-level workspace bootstrap, build, test, run, and genesis helpers for the active daemon.
- `cmd/unified-node`: persistent node daemon with BadgerDB-backed chain state, JSON-RPC, governance endpoints, and libp2p networking.
- `cmd/unified-cli`: governance CLI for listing proposals and casting votes.
- `web3/desktop`: Electron + React desktop wallet/explorer for local key management, transfers, UNS registration, and crawl-ledger visibility.
- `core/blockchain.go`: ledger persistence, transaction/state transitions, search index storage, system-contract accessors, and architect fee enforcement.
- `core/engine.go`: mempools plus the PoUW mining loop.
- `core/system_contracts.go`: explicit genesis-deployed protocol contract registry for `0x101` and `0x102`, including ABI metadata and descriptor bytecode for introspection.
- `api/rpc_server.go`: JSON-RPC methods for balances, transfers, blocks, search task submission, local search-index reads, contract introspection, native contract reads, and network metadata.
- `contracts/UNS.sol`: UNS registry contract that prices names from the `0x101` search precompile mention frequency.

## Launch Readiness

- `Improved`: remote blocks now pass local PoUW validator-quorum checks before import, peers can sync missing blocks over libp2p, governance state survives node restarts, the node persists side branches with cumulative-work-based reorgs onto the heavier canonical chain, and ingress paths now enforce bounded mempools plus basic RPC/P2P rate limits.
- `Improved`: `0x101` and `0x102` now exist as explicit genesis-deployed protocol contracts with `eth_getCode`, `ufi_getContract`, and `ufi_listContracts` introspection instead of only ad hoc switch-based routing.
- `Improved`: nodes can now load and persist an explicit shared network manifest with chain ID, genesis address, architect treasury, bootnodes, and protocol contract metadata; RPC exposes that manifest through `ufi_getNetworkConfig` and `eth_chainId`.
- `Improved`: the P2P stack now tracks peer reputation, applies adaptive gossip/sync budgets, disconnects repeatedly abusive peers, and exposes live peer status at `/p2p/peers`.
- `Improved`: block work now discounts repeated same-host and same-submitter crawl proofs while preserving deterministic replay across validators and reorgs.
- `Operational gate`: launch decisions can now use `/readyz`, which reports whether the current node configuration clears the built-in production-readiness checks.
- `Operational requirement`: all nodes on the same network must share the same network manifest, or at minimum the same chain ID, genesis address, architect address, and circulating supply, or they will form incompatible chains.

## Workspace Setup

Bootstrap dependencies and local directories:

```bash
./setup.sh
```

Common targets:

```bash
make test
make build
make run-node
make run-mine
make check-node
make generate-network-config
sudo make configure-firewall-linux
sudo make install-backup-rotation
make verify-network-config
make print-cutover-commands
make desktop-install
make desktop-build
sudo make install-go-linux
sudo make install-seed-node
sudo make install-seed-node-macos
```

The full operator runbook is in `docs/runbook.md`.

A pinned mainnet manifest example is in [unified-network.mainnet.example.json](/Users/efrainvera/Documents/UNIFIED/config/unified-network.mainnet.example.json). Generate your real manifest from it, do not hand-edit per-node values into different copies.

## Run The Node

Prerequisite: Go 1.25+

```bash
go run ./cmd/unified-node
```

Common flags:

```bash
go run ./cmd/unified-node \
  --network-config ./config/unified-network.json \
  --datadir ./data \
  --rpcport 8545 \
  --mine \
  --operator UFI_LOCAL_OPERATOR \
  --operator-alias local-operator \
  --operator-voting-power 5000
```

The daemon mounts governance REST endpoints and JSON-RPC on the same HTTP server. JSON-RPC is available on `/rpc`, while `/healthz`, `/readyz`, `/p2p/peers`, `/governance/*`, `/chain/*`, and `/consensus/quote` remain available for the existing CLI and governance tooling.

For multi-node operation, every peer should start from the same network config JSON. You can generate one with:

```bash
make generate-network-config \
  NETWORK_CONFIG=./config/unified-network.json \
  NETWORK_NAME=unified-mainnet \
  CHAIN_ID=333 \
  GENESIS_ADDRESS=UFI_SHARED_GENESIS_FUNDER \
  ARCHITECT_ADDRESS=UFI_SHARED_ARCHITECT \
  CIRCULATING_SUPPLY=1000000 \
  BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW...
```

## Seed Node Server Install

For a Linux server that should stay online as a long-running seeding node, use the systemd installer in [install_seed_node.sh](/Users/efrainvera/Documents/UNIFIED/scripts/ops/install_seed_node.sh). It builds `unified-node` from this repo, installs the binary under `/usr/local/bin`, creates `/etc/unified/unified-seed-node.env`, writes `/etc/unified/unified-network.json`, writes a systemd unit, and enables the service.

If the server does not already have Go installed, install it first with the official tarball helper in [install_go_linux.sh](/Users/efrainvera/Documents/UNIFIED/scripts/ops/install_go_linux.sh):

```bash
cd /Users/efrainvera/Documents/UNIFIED
sudo make install-go-linux
```

Example install:

```bash
cd /Users/efrainvera/Documents/UNIFIED

sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  ./scripts/ops/install_seed_node.sh
```

Start immediately after install:

```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  ./scripts/ops/install_seed_node.sh --start --overwrite-env
```

If you already generated one pinned manifest file, copy it to the host and install from that exact file instead of re-rendering from env values:

```bash
sudo UNIFIED_NETWORK_CONFIG_SOURCE=./config/unified-network.mainnet.json \
  UNIFIED_OPERATOR_ADDRESS=UFI_REAL_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  ./scripts/ops/install_seed_node.sh --start --overwrite-env
```

Important defaults:

- RPC stays on `127.0.0.1:8545` unless you change `UNIFIED_RPC_HOST`.
- P2P listens on `/ip4/0.0.0.0/tcp/4001`.
- The installer refuses to start the service if either the env file or the generated network config still contains `REPLACE_ME` placeholders.

Recommended next production steps on Linux:

```bash
sudo make configure-firewall-linux P2P_PORT=4001 SSH_PORT=22 RPCPORT=8545
sudo make install-backup-rotation DATADIR=/var/lib/unified BACKUP_DIR=/var/backups/unified BACKUP_RETENTION=14
```

Template files are in [unified-seed-node.env.tmpl](/Users/efrainvera/Documents/UNIFIED/deploy/env/unified-seed-node.env.tmpl), [unified-network.json.tmpl](/Users/efrainvera/Documents/UNIFIED/deploy/network/unified-network.json.tmpl), and [unified-seed-node.service.tmpl](/Users/efrainvera/Documents/UNIFIED/deploy/systemd/unified-seed-node.service.tmpl).

## Seed Node Install On macOS

The Linux installer will not run on macOS because it depends on `systemd`. For macOS use [install_seed_node_macos.sh](/Users/efrainvera/Documents/UNIFIED/scripts/ops/install_seed_node_macos.sh), which installs the node as a `launchd` LaunchDaemon.

Install Go first if needed:

```bash
brew install go
```

Then install the seed node service:

```bash
cd /Users/efrainvera/Documents/UNIFIED

sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/66.163.125.129/tcp/4001/p2p/<peer-id> \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  ./scripts/ops/install_seed_node_macos.sh --start --overwrite-env
```

Pinned-manifest install on macOS:

```bash
sudo UNIFIED_NETWORK_CONFIG_SOURCE=./config/unified-network.mainnet.json \
  UNIFIED_OPERATOR_ADDRESS=UFI_REAL_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  ./scripts/ops/install_seed_node_macos.sh --start --overwrite-env
```

You can also invoke it through:

```bash
sudo make install-seed-node-macos
```

The macOS installer writes a LaunchDaemon plist from [io.unified.seed-node.plist.tmpl](/Users/efrainvera/Documents/UNIFIED/deploy/launchd/io.unified.seed-node.plist.tmpl), a managed env file, a shared `unified-network.json`, and a wrapper script that launches `unified-node` with the configured flags.

## Desktop Explorer / Wallet

The desktop app is in `web3/desktop`. It derives UFI addresses locally from an Ed25519 seed, signs raw transactions inside the Electron preload context, resolves UNS names, registers new UNS names, and shows live chain/search/governance state from the connected node.

Install dependencies once:

```bash
make desktop-install
```

Run the desktop app in development mode:

```bash
make desktop-dev
```

Build the production renderer bundle:

```bash
make desktop-build
```

Launch Electron against the built bundle:

```bash
make desktop-start
```

The default RPC endpoint is `http://127.0.0.1:8545`. You can change it inside the app without restarting. The wallet key is session-local to the desktop process and is not posted to the node.

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

Quote the current UNS registration price:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":4,"method":"ufi_getNamePrice","params":{"name":"Architect"}}'
```

Call the native `0x101` search precompile directly:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":5,"method":"ufi_callNative","params":{"to":"0x101","data":"0x1e6f732d000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000094172636869746563740000000000000000000000000000000000000000000000"}}'
```

Read the active shared network config:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":6,"method":"ufi_getNetworkConfig","params":{}}'
```

Verify that a running node matches the pinned manifest:

```bash
make verify-network-config \
  NETWORK_CONFIG=./config/unified-network.mainnet.json
```

Verify a live node against that same pinned manifest:

```bash
make verify-network-config \
  NETWORK_CONFIG=./config/unified-network.mainnet.json \
  VERIFY_RPC_URL=http://127.0.0.1:8545
```

Print exact bootstrap or joiner cutover commands from the pinned manifest:

```bash
make print-cutover-commands \
  NETWORK_CONFIG=./config/unified-network.mainnet.json \
  PLATFORM=linux \
  ROLE=bootstrap \
  OPERATOR=UFI_REAL_OPERATOR \
  OPERATOR_ALIAS=mainnet-seed-1
```

Read the chain ID:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":7,"method":"eth_chainId","params":[]}'
```

Read the node readiness gate:

```bash
curl -s http://127.0.0.1:8545/readyz
```

Inspect peer reputation and adaptive-limit state:

```bash
curl -s http://127.0.0.1:8545/p2p/peers
```

The staged production cutover sequence is documented in [runbook.md](/Users/efrainvera/Documents/UNIFIED/docs/runbook.md).

Use the generic read-only call path:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":6,"method":"ufi_call","params":{"to":"0x102","data":"0x8bdd48cc000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000094172636869746563740000000000000000000000000000000000000000000000","block":"latest"}}'
```

Compatibility-style `eth_call` also works:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":7,"method":"eth_call","params":[{"to":"0x102","data":"0x8bdd48cc000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000094172636869746563740000000000000000000000000000000000000000000000"},"0x0"]}'
```

List the deployed system contracts:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":8,"method":"ufi_listContracts","params":{}}'
```

Fetch a contract record:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":9,"method":"ufi_getContract","params":{"address":"0x102"}}'
```

Inspect descriptor bytecode with `eth_getCode`:

```bash
curl -s -X POST http://127.0.0.1:8545/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":10,"method":"eth_getCode","params":["0x102","latest"]}'
```

The returned code is deterministic descriptor bytecode for tooling and deployment introspection. The node still executes these addresses through the protocol system-contract registry rather than a general-purpose EVM.

## Genesis Script

The genesis bootstrap script now asks the node for the live UNS registration quote, signs the Architect registration locally, broadcasts it through `ufi_sendRawTransaction`, waits for block `#1`, and then submits the first crawl seed task.

Single-command bootstrap:

```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make bootstrap-architect \
  DATADIR=./data/genesis-architect
```

This derives the Architect UFI address from the key, starts a mining node in the background, waits for `/healthz`, runs the genesis transaction, and leaves the node running. Logs are written under `./logs/`.

Run the node with the Architect address used as the shared genesis address, then execute:

```bash
go run ./cmd/unified-node --mine --operator <architect-ufi-address> --genesis-address <architect-ufi-address>

UFI_ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
UFI_RPC_URL=http://127.0.0.1:8545 \
go run ./scripts/genesis_tx
```

Optional overrides:

```bash
UFI_ARCHITECT_ADDRESS=<expected-ufi-address> \
UFI_GENESIS_URL=https://unified.network/genesis \
UFI_GENESIS_QUERY="UniFied genesis seed" \
go run ./scripts/genesis_tx
```

## Bulk URL Seeding

Create a file with one URL per line. You can also use `url,query` lines to override the default query for specific URLs.

Example `urls.txt`:

```text
https://example.com
https://openai.com,ai research
https://mit.edu
```

Seed the node with live governance-aware bounty quotes:

```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make seed-urls \
  URLS_FILE=./urls.txt \
  SEED_QUERY="initial web seed" \
  SEED_BASE_BOUNTY=1.0 \
  SEED_DIFFICULTY=8 \
  SEED_DATA_VOLUME_BYTES=1024
```

The seeder uses the current `/consensus/quote` rules per URL, enqueues tasks in sender-safe batches, and waits until the tasks are mined. With the current sender limit, one account can keep at most `32` tasks in flight at once.

## Operations

Check a running node:

```bash
make check-node
```

Back up a datadir:

```bash
make backup-datadir DATADIR=./data/local
```

Restore a backup archive:

```bash
make restore-datadir BACKUP_ARCHIVE=./data-local-20260317-120000.tar.gz RESTORE_TARGET=./data/restored
```

Run a build-plus-backup rollout:

```bash
make rollout-node DATADIR=./data/local
```

For an automatic restart, set `UFI_RESTART=1` and optionally `PID_FILE=./run/unified-node.pid`.

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
