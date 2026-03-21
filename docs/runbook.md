# 📖 UniFied Protocol Runbook

This guide serves as the official operational manual for the UniFied prototype daemon. It covers deployment, security, and maintenance for a **Proof of Useful Work (PoUW)** search indexing network.

---

## 📋 Prerequisites
Before starting, ensure your environment meets the following requirements:
* **Language**: Go 1.25 or newer
* **Tools**: `make`, `curl`, `git`
* **Optional**: `npx` (only for local Solidity artifact compilation)

**Initialize Workspace:**
```bash
./setup.sh
```

---

## 🖥️ Single-Node Devnet
Perfect for local testing and debugging.

### 1. Passive Node (No Mining)
Starts a local node that tracks the chain but does not participate in PoUW.
```bash
make run-node \
  DATADIR=./data/devnet-a \
  OPERATOR=UFI_LOCAL_OPERATOR \
  GENESIS_ADDRESS=UFI_LOCAL_OPERATOR
```

### 2. Active Node (Mining Enabled)
Starts a local node with the 15-second PoUW mining loop active.
```bash
make run-mine \
  DATADIR=./data/devnet-a \
  OPERATOR=UFI_LOCAL_OPERATOR \
  GENESIS_ADDRESS=UFI_LOCAL_OPERATOR
```

### 🔍 Health Checks
```bash
make smoke-health
make smoke-rpc
curl -s [http://127.0.0.1:3337/readyz](http://127.0.0.1:3337/readyz)
```

---

## 🌐 Multi-Node Bootstrap
Every node on the same network **must** share an identical configuration. At minimum, the following must match:
`CHAIN_ID` | `GENESIS_ADDRESS` | `ARCHITECT_ADDRESS` | `CIRCULATING_SUPPLY`

### Setup Sequence:
1. **Start Node A**: Launch and capture the libp2p listen address from the logs.
2. **Start Node B**: Use Node A as a bootnode.
```bash
make run-node \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  DATADIR=./data/devnet-b \
  OPERATOR=UFI_NODE_B \
  BOOTNODES=/ip4/127.0.0.1/tcp/4001/p2p/12D3KooW...
```

---

## 🚀 Pre-Launch Mainnet Checklist
Freeze these values before the first server installation. Once the genesis block is mined, these cannot be changed without a hard fork.

| Category | Item |
| :--- | :--- |
| **Identity** | Genesis & Architect addresses must be real UFI addresses. |
| **Network** | `CHAIN_ID`, `CIRCULATING_SUPPLY`, and `BOOTNODES` must be frozen. |
| **Security** | Firewall policy ready; RPC bound to `127.0.0.1`. |
| **Redundancy** | At least one non-bootstrap backup node available. |

**Generate Pinned Manifest:**
```bash
make generate-network-config \
  NETWORK_CONFIG=./config/networks/unified-network.mainnet.json \
  NETWORK_NAME=unified-mainnet \
  CHAIN_ID=333 \
  GENESIS_ADDRESS=UFI_SHARED_GENESIS \
  ARCHITECT_ADDRESS=UFI_SHARED_ARCHITECT
```

---

## 🧱 Linux Firewall (UFW)
Apply a strict policy before the service starts.
```bash
sudo make configure-firewall-linux \
  P2P_PORT=4001 \
  SSH_PORT=22 \
  RPCPORT=3337 \
  ALLOW_RPC_PUBLIC=0
```
* **SSH**: Allowed inbound.
* **P2P**: Open for libp2p GossipSub.
* **RPC**: Kept private by default.

---

## 📦 Seed Node Server Install (Linux)
Install the node as a persistent `systemd` service.

**Install and Start:**
```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_SEED_OPERATOR \
  UNIFIED_CIRCULATING_SUPPLY=1000000 \
  ./scripts/ops/install_seed_node.sh --start --overwrite-env
```

**Default File Paths:**
* **Binary**: `/usr/local/bin/unified-node`
* **Config**: `/etc/unified/unified-network.json`
* **Service**: `journalctl -u unified-seed-node`

---

## 🍎 Seed Node Install On macOS
Uses `launchd` LaunchDaemons.
```bash
sudo make install-seed-node-macos
```
**Logs**: `tail -f /usr/local/var/log/unified/unified-seed-node.out.log`

---

## 🐣 Genesis Bootstrap
The Architect must register their identity and submit the first seed task to finalize block #1.

```bash
ARCHITECT_KEY=<hex-ed25519-key> \
make bootstrap-architect \
  NETWORK_CONFIG=./config/networks/unified-network.local.json \
  DATADIR=./data/genesis-architect
```

---

## 🔍 Bulk URL Seeding
Populate the ledger by seeding verified URLs. Prepare a `urls.txt` (format: `url,query`).

```bash
SEED_KEY=<hex-key> \
make seed-urls \
  URLS_FILE=./testdata/seeds/urls.txt \
  SEED_QUERY="initial web seed" \
  SEED_PREFLIGHT=true
```
> ⚠️ **Note**: One sender can keep 32 tasks in flight. The seeder will automatically skip `403 Forbidden` sites during preflight.

---

## 🛠️ Operations Toolkit

| Command | Purpose |
| :--- | :--- |
| `make check-node` | Deep surface scan of node health. |
| `make backup-datadir` | Archive the chain state and BadgerDB. |
| `make rollout-node` | Safe build + backup + restart sequence. |
| `curl ... /p2p/peers` | View real-time peer reputation scores. |

---

## 🔄 Upgrade & Recovery
* **Wipe & Sync**: If the shared manifest changes, wipe the datadir and restart.
* **Persistence**: Governance state is stored in the datadir; never delete it unless doing a full network reset.
* **Shutdown**: Always use `SIGTERM` or `Ctrl+C` to ensure BadgerDB flushes correctly.

---
<p align="center">
  <b>UniFied Protocol Ops © 2026</b><br/>
  <i>Maintaining the Ledger of Truth.</i>
</p>
