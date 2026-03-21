# UniFied Runbook

This runbook covers the current prototype daemon in this repository. It assumes a shared network config JSON, local BadgerDB state, JSON-RPC on `/rpc`, readiness checks on `/readyz`, and the optional PoUW mining loop.

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
curl -s http://127.0.0.1:3337/readyz
```

## Multi-Node Bootstrap

Every node on the same network must share the same network config, or at minimum the same:

- `CHAIN_ID`
- `GENESIS_ADDRESS`
- `ARCHITECT_ADDRESS`
- `CIRCULATING_SUPPLY`

Start node A and copy one of the printed libp2p listen addresses:

```bash
make run-mine \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  DATADIR=./data/devnet-a \
  OPERATOR=UFI_NODE_A
```

Start node B with node A as a bootnode:

```bash
make run-node \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  DATADIR=./data/devnet-b \
  OPERATOR=UFI_NODE_B \
  BOOTNODES=/ip4/127.0.0.1/tcp/4001/p2p/12D3KooW...
```

## Pre-Launch Mainnet Checklist

Freeze these shared values before any server is installed:

- `NETWORK_NAME`
- `CHAIN_ID`
- `GENESIS_ADDRESS`
- `ARCHITECT_ADDRESS`
- `CIRCULATING_SUPPLY`
- initial `BOOTNODES`
- the exact `unified-network.json` payload every node will consume

Generate the shared network manifest once from a clean workstation and distribute that exact file unchanged:

```bash
make generate-network-config \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  NETWORK_NAME=unified-mainnet \
  CHAIN_ID=333 \
  GENESIS_ADDRESS=UFI_REAL_SHARED_GENESIS \
  ARCHITECT_ADDRESS=UFI_REAL_SHARED_ARCHITECT \
  CIRCULATING_SUPPLY=1000000 \
  BOOTNODES=
```

Keep that file as the pinned launch manifest and copy it unchanged to every host. A starter example lives at [unified-network.mainnet.example.json](/Users/efrainvera/Documents/UNIFIED/config/networks/unified-network.mainnet.example.json), but the real launch file should be generated once and then frozen.

When you are ready to hand off the mainnet launch package, build a versioned release bundle:

```bash
make package-mainnet-release \
  RELEASE_NETWORK_CONFIG=./config/networks/unified-network.mainnet.json \
  RELEASE_TARGETS='linux/amd64 linux/arm64 darwin/arm64'
```

This produces runtime archives, an ops bundle, website bundle, desktop bundle, `SHA256SUMS`, `release-manifest.json`, and a top-level `unified-mainnet-launch-<version>.tar.gz` under `./build/release/<version>/`.

Generate each node operator identity separately and store the seed offline:

```bash
make generate-operator
```

Before opening the network to outside peers, confirm:

- the designated genesis/architect addresses are real UFI addresses, not placeholders
- every server has the same `unified-network.json`
- RPC is planned to stay on `127.0.0.1`
- backup destination and retention are chosen
- firewall policy is ready before the service starts
- at least one non-bootstrap backup node is available

## Linux Firewall

For a Linux seed node, apply a minimal `ufw` policy before starting the service:

```bash
sudo make configure-firewall-linux \
  P2P_PORT=4001 \
  SSH_PORT=22 \
  RPCPORT=3337 \
  ALLOW_RPC_PUBLIC=0
```

This helper:

- allows inbound SSH
- allows inbound libp2p on the configured P2P port
- keeps RPC private by default
- enables `ufw` with inbound deny / outbound allow defaults

If you intentionally expose RPC through a reverse proxy or separate ACL layer, make that a deliberate change and document it separately.

## Seed Node Server Install

For a persistent Linux server, install the node as a systemd service instead of running it in a shell.

If `go` is missing on the host, install it first:

```bash
sudo make install-go-linux
```

Dry-run the installer:

```bash
sudo UNIFIED_DRY_RUN=1 \
  UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  make install-seed-node
```

Install and write the env file plus systemd unit:

```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  make install-seed-node
```

Install and start immediately:

```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  ./scripts/ops/install_seed_node.sh --start --overwrite-env
```

Pinned-manifest install path:

```bash
sudo UNIFIED_NETWORK_CONFIG_SOURCE=./config/networks/unified-network.mainnet.json \
  UNIFIED_OPERATOR_ADDRESS=UFI_REAL_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  ./scripts/ops/install_seed_node.sh --start --overwrite-env
```

Installed paths by default:

- Binary: `/usr/local/bin/unified-node`
- Network config: `/etc/unified/unified-network.json`
- Env file: `/etc/unified/unified-seed-node.env`
- Systemd unit: `/etc/systemd/system/unified-seed-node.service`
- Datadir: `/var/lib/unified`
- Logs: `journalctl -u unified-seed-node`

Operational notes:

- Keep `UNIFIED_RPC_HOST=127.0.0.1` unless you intentionally expose RPC behind access controls.
- If a browser app must call the node directly, set `UNIFIED_RPC_CORS_ORIGINS` to a specific origin or `*`, and review firewall/rate-limit exposure before binding RPC publicly.
- Replace placeholder `REPLACE_ME` values in both the env file and the shared network config before starting.
- All shared-network nodes still need the exact same chain ID, genesis address, architect address, and circulating supply.
- If you need different paths or service names, override the documented `UNIFIED_*` installer variables.

## Backup Rotation

For Linux nodes, install an automatic backup timer after the seed node service exists:

```bash
sudo make install-backup-rotation \
  DATADIR=/var/lib/unified \
  BACKUP_DIR=/var/backups/unified \
  BACKUP_RETENTION=14 \
  BACKUP_ON_CALENDAR='*-*-* 02:15:00'
```

That installs:

- `/usr/local/libexec/unified/rotate_backups.sh`
- `/etc/systemd/system/unified-backup.service`
- `/etc/systemd/system/unified-backup.timer`

Verify the timer:

```bash
sudo systemctl status unified-backup.timer
sudo systemctl list-timers unified-backup.timer
```

You can still create an immediate manual archive:

```bash
make backup-datadir DATADIR=/var/lib/unified BACKUP_OUTPUT=./unified-manual-backup.tar.gz
```

## Seed Node Install On macOS

The Linux installer does not work on macOS because there is no `systemd`. Use the macOS LaunchDaemon installer instead.

Install Go if it is missing:

```bash
brew install go
```

Install and start the LaunchDaemon:

```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  UNIFIED_BOOTNODES=/ip4/66.163.125.129/tcp/4001/p2p/<peer-id> \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  ./scripts/ops/install_seed_node_macos.sh --start --overwrite-env
```

Or through `make`:

```bash
sudo make install-seed-node-macos
```

Pinned-manifest install path on macOS:

```bash
sudo UNIFIED_NETWORK_CONFIG_SOURCE=./config/networks/unified-network.mainnet.json \
  UNIFIED_OPERATOR_ADDRESS=UFI_REAL_OPERATOR \
  UNIFIED_OPERATOR_ALIAS=mainnet-seed-1 \
  ./scripts/ops/install_seed_node_macos.sh --start --overwrite-env
```

Default macOS paths:

- Binary: `/usr/local/bin/unified-node`
- Network config: `/usr/local/etc/unified/unified-network.json`
- Env file: `/usr/local/etc/unified/unified-seed-node.env`
- Wrapper: `/usr/local/libexec/unified/unified-seed-node-launch.sh`
- LaunchDaemon: `/Library/LaunchDaemons/io.unified.seed-node.plist`
- Data dir: `/usr/local/var/lib/unified`
- Logs: `/usr/local/var/log/unified/unified-seed-node.out.log` and `/usr/local/var/log/unified/unified-seed-node.err.log`

Useful commands:

```bash
sudo launchctl print system/io.unified.seed-node
sudo launchctl kickstart -k system/io.unified.seed-node
tail -f /usr/local/var/log/unified/unified-seed-node.out.log /usr/local/var/log/unified/unified-seed-node.err.log
```

## Staged Mainnet Cutover

1. Generate the final shared `unified-network.json` and copy it to every node host unchanged.
2. Generate a unique operator identity for each node and keep each seed offline.
3. Verify the pinned manifest locally before touching a host:

```bash
make verify-network-config NETWORK_CONFIG=./config/networks/unified-network.mainnet.json
```

4. On the first Linux seed node, apply the firewall policy:

```bash
sudo make configure-firewall-linux P2P_PORT=4001 SSH_PORT=22 RPCPORT=3337
```

5. Install the node service on the first seed node with `UNIFIED_BOOTNODES` empty.
6. Install backup rotation on that same node.
7. Start the node and verify:

```bash
curl -s http://127.0.0.1:3337/healthz
curl -s http://127.0.0.1:3337/readyz
curl -s http://127.0.0.1:3337/p2p/peers
```

8. Capture the first node's full libp2p multiaddr from the logs and freeze it as the initial bootnode:

```bash
journalctl -u unified-seed-node -n 50 --no-pager | grep 'p2p listen address:'
```

9. Re-render the shared manifest with that bootnode if you did not already bake it in, then redistribute the final manifest to the remaining nodes.
10. Install node B and node C with the same manifest and with `UNIFIED_BOOTNODES` set to the full multiaddr from node A.
11. Verify all nodes agree on chain ID, shared manifest, and peer visibility:

```bash
curl -s http://127.0.0.1:3337/p2p/peers
curl -s -X POST http://127.0.0.1:3337/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ufi_getNetworkConfig","params":{}}'
```

12. Run the pinned-manifest verifier against each live node:

```bash
make verify-network-config \
  NETWORK_CONFIG=./config/networks/unified-network.mainnet.json \
  VERIFY_RPC_URL=http://127.0.0.1:3337
```

13. Only after the network is stable, run the genesis bootstrap on the designated architect node.
14. Confirm block `#1`, the Architect UNS registration, and the seed task before announcing the network.

Rollback guidance:

- if the shared manifest is wrong, stop all nodes and wipe the datadirs before restart
- if only one node is misconfigured, do not let it continue syncing against production peers; correct the manifest locally first
- if `/readyz` returns non-OK status for production nodes, treat that as a launch blocker, not a warning to ignore

If you want the exact shell block for a given role before touching a host, generate it from the pinned manifest:

```bash
make print-cutover-commands \
  NETWORK_CONFIG=./config/networks/unified-network.mainnet.json \
  PLATFORM=linux \
  ROLE=bootstrap \
  OPERATOR=UFI_REAL_OPERATOR \
  OPERATOR_ALIAS=mainnet-seed-1
```

## Genesis Bootstrap

Fastest path:

```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make bootstrap-architect \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  DATADIR=./data/genesis-architect
```

That script derives the Architect address from the supplied key, starts the node in the background with mining enabled, waits for the local health check, runs the genesis registration and seed task, and leaves the node running. Node logs are written to `./logs/architect-bootstrap-*.log`.

Manual path:

1. Start the node with mining enabled and the architect address as both `OPERATOR` and `GENESIS_ADDRESS`.
2. Export the architect private key.
3. Run the genesis script to register the `Architect` UNS name and submit the seed crawl task.

```bash
make run-mine \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  DATADIR=./data/genesis \
  OPERATOR=<architect-ufi-address>
```

In a second shell:

```bash
make genesis \
  RPCPORT=3337 \
  ARCHITECT_KEY=<hex-or-base64-ed25519-key>
```

Optional seed overrides:

```bash
make genesis \
  RPCPORT=3337 \
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

Install the desktop wallet/explorer dependencies:

```bash
make desktop-install
```

Build the desktop renderer bundle:

```bash
make desktop-build
```

Compile the UNS Solidity artifact:

```bash
make solc-uns
```

## Desktop Wallet / Explorer

The Electron wallet/explorer lives under `web/desktop` and expects a running UniFied node on `http://127.0.0.1:3337` unless you override the RPC URL in the app.

Start the desktop app in development mode:

```bash
make desktop-dev
```

Start Electron against the built bundle:

```bash
make desktop-start
```

Current desktop capabilities:

- Import or generate a local Ed25519 session key.
- Derive the UFI address locally and inspect balance plus latest/pending nonce.
- Resolve and reverse-resolve UNS names.
- Sign and broadcast standard transfers.
- Quote and sign UNS registrations.
- Inspect blocks, search indexed pages, view system contracts, and watch governance rules/proposals.

## Contract Introspection

The node exposes the protocol system contracts as first-class introspection targets.

List them:

```bash
curl -s -X POST http://127.0.0.1:3337/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ufi_listContracts","params":{}}'
```

Fetch one contract record:

```bash
curl -s -X POST http://127.0.0.1:3337/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"ufi_getContract","params":{"address":"0x102"}}'
```

Inspect descriptor bytecode:

```bash
curl -s -X POST http://127.0.0.1:3337/rpc \
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
SEED_KEY=<hex-or-base64-ed25519-key> \
make seed-urls \
  URLS_FILE=./testdata/seeds/urls.txt \
  SEED_QUERY="initial web seed" \
  SEED_BASE_BOUNTY=1.0 \
  SEED_DIFFICULTY=8 \
  SEED_DATA_VOLUME_BYTES=1024 \
  SEED_PREFLIGHT=true
```

Operational notes:

- The sender account must have enough UFD for the full quoted bounty set.
- The seeder preflights URLs by default and skips obvious failures like persistent `403` sites before quoting and submission.
- The script refuses to start if the sender already has pending transactions.
- One sender can keep at most `32` tasks in flight with the current node limits, so large seeds are submitted in waves while the miner drains the queue.

## Operations Toolkit

Health and surface check:

```bash
make check-node
```

Peer reputation view:

```bash
curl -s http://127.0.0.1:3337/p2p/peers
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
- The built-in readiness gate covers code and configuration only; it does not replace external audits, staged load testing, or operational key-management reviews.
