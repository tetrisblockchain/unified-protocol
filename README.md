# 🌐 UniFied Protocol (UFI)
### The Decentralized Ledger for Global Web Indexing

**UniFied** is a high-performance Layer 1 blockchain specifically engineered to decentralize the act of search indexing. By replacing arbitrary "math puzzles" with **Proof of Useful Work (PoUW)**, UniFied transforms network energy into a verifiable, global map of human knowledge.

---

## 🏗️ Architecture & Core Components

UniFied is built on a modular Go-based architecture, utilizing **libp2p** for GossipSub block propagation and **BadgerDB** for high-speed ledger persistence.

| Component | Icon | Description |
| :--- | :---: | :--- |
| **State Engine** | ⚙️ | Persistent blockchain state with 15s block times. |
| **PoUW Miner** | ⛏️ | Mining loop that crawls and indexes the web via Go-Colly. |
| **UNS Registry** | 🆔 | Decentralized identity priced by internet mention frequency. |
| **System Contracts** | 📜 | Hard-coded registry for `0x101` (Search) and `0x102` (UNS). |
| **GossipSub** | 📡 | P2P block and transaction propagation via libp2p. |
| **Desktop Wallet** | 💻 | Electron + React dashboard for local key management. |

---

## 💎 The Economic Moat: Architect Revenue

The protocol enforces a hard-coded **3.33% Architect Fee** at the consensus level to ensure long-term sustainability.

* **🔒 Immutable Distribution:** 3.33% of every block reward and search bounty is automatically routed to the Genesis Architect Treasury.
* **📈 Value Capture:** All UNS registrations and governance stakes contribute to the protocol's development fund, ensuring continuous innovation without external reliance.

---

## 🚀 Workspace Setup

**Prerequisites:** * **Go 1.25+** (Required for performance optimizations)
* **Node.js** (For Desktop Wallet)

```bash
# Bootstrap dependencies and local directories
./setup.sh

# Common Build Targets
make build        # Compile unified-node and unified-cli
make test         # Run PoUW and state logic tests
make check-node   # Verify health of a running node
```

---

## 🏗️ Network Configuration (Mainnet)

For multi-node operation, all peers must share an identical network manifest. You can generate a pinned manifest file to ensure chain compatibility:

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

---

## ⛏️ Running the Node

Launch a mining node using your local operator address:

```bash
go run ./cmd/unified-node \
  --network-config ./config/unified-network.json \
  --datadir ./data \
  --rpcport 8545 \
  --mine \
  --operator UFI_LOCAL_OPERATOR \
  --operator-alias mainnet-miner-01
```

---

## 📡 Production Seed Node Setup

UniFied provides official systemd (Linux) and launchd (macOS) installers for long-running seed nodes.

### 🐧 Linux Server Install
```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  UNIFIED_BOOTNODES=/ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  ./scripts/ops/install_seed_node.sh --start --overwrite-env
```

### 🍎 macOS Server Install
```bash
sudo UNIFIED_GENESIS_ADDRESS=UFI_SHARED_MAINNET_GENESIS \
  UNIFIED_ARCHITECT_ADDRESS=UFI_SHARED_MAINNET_ARCHITECT \
  UNIFIED_OPERATOR_ADDRESS=UFI_MAINNET_SEED_OPERATOR \
  ./scripts/ops/install_seed_node_macos.sh --start --overwrite-env
```

---

## 🔍 JSON-RPC API Reference

The node exposes a rich API for search data introspection and system contract interaction.

**List Deployed System Contracts:**
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -d '{"jsonrpc":"2.0","id":8,"method":"ufi_listContracts","params":{}}'
```

**Query Indexed Web Content:**
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -d '{"jsonrpc":"2.0","id":3,"method":"ufi_getSearchData","params":{"url":"[https://example.edu](https://example.edu)","term":"blockchain"}}'
```

---

## ⚙️ Operations & Maintenance

Keep your node healthy with built-in backup and security tools:

* **🔒 Firewall:** `sudo make configure-firewall-linux P2P_PORT=4001 SSH_PORT=22`
* **💾 Backups:** `sudo make install-backup-rotation DATADIR=/var/lib/unified`
* **🔄 Rollouts:** `make rollout-node DATADIR=./data/local`

---

## 🛡️ Launch Readiness Status


* **✅ Improved:** Peers sync missing blocks over libp2p with reputation tracking.
* **✅ Improved:** System contracts `0x101`/`0x102` support explicit `eth_getCode` introspection.
* **✅ Improved:** Validated PoUW work discounts repeated same-host crawl proofs.
* **🚧 Missing:** Advanced peer reputation and adaptive abuse controls (v1.1 Roadmap).

---

## ⚖️ License

Licensed under the **GNU General Public License v3.0 (GPL-3.0)**. See [LICENSE](LICENSE) for details.

**Architect:** [ufi.network](https://ufi.network) | 2026
