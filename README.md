# UniFied Protocol (UFI) 🌐
### The Decentralized Ledger for Global Web Indexing

**UniFied** is a high-performance Layer 1 blockchain prototype designed to decentralize search indexing. By replacing traditional Proof of Work with **Proof of Useful Work (PoUW)**, UniFied transforms energy consumption into a global, verifiable, and uncensorable map of human knowledge.

The current workspace includes a persistent blockchain state engine, a 15-second mining loop, JSON-RPC, governance-aware crawl priority rules, and libp2p GossipSub block propagation.

---

## 🏛️ Core Components

- **`cmd/unified-node`**: Persistent node daemon with BadgerDB-backed chain state, JSON-RPC, governance endpoints, and libp2p networking.
- **`cmd/unified-cli`**: Governance CLI for listing proposals and casting votes.
- **`core/blockchain.go`**: Ledger persistence, transaction/state transitions, search index storage, system-contract accessors, and **3.33% Architect Fee** enforcement.
- **`core/engine.go`**: Mempools plus the PoUW mining loop.
- **`core/system_contracts.go`**: Explicit system-contract registry for `0x101` and `0x102`, including ABI metadata and descriptor bytecode.
- **`api/rpc_server.go`**: JSON-RPC methods for balances, search task submission, contract introspection, and native contract reads.
- **`contracts/UNS.sol`**: UNS registry contract that prices names based on `0x101` search precompile mention frequency.

---

## 💎 Economic Engine: The Architect's Share

To ensure long-term sustainability and continuous innovation, UniFied implements a hard-coded **3.33% Architect Fee** at the consensus level.

* **Sustainability:** 3.33% of every block reward, search bounty, and UNS registration is automatically routed to the **Genesis Architect Address**.
* **Immutability:** This fee is a core state transition rule. Any block failing to distribute this share is rejected by the network.

---

## 🚀 Workspace Setup

**Prerequisite:** Go 1.25+

Bootstrap dependencies and local directories:
```bash
./setup.sh
```

### Common Build Targets
```bash
make test        # Run PoUW and state logic tests
make build       # Compile unified-node and unified-cli
make run-node    # Launch the daemon
make run-mine    # Launch daemon with mining enabled
make check-node  # Verify health of a running node
```

---

## 🏗️ Genesis Bootstrap

**Single-command bootstrap:**
```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make bootstrap-architect \
  DATADIR=./data/genesis-architect
```

---

## 🔍 Bulk URL Seeding

Seed the node with live governance-aware bounty quotes:
```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make seed-urls \
  URLS_FILE=./urls.txt \
  SEED_QUERY="initial web seed" \
  SEED_BASE_BOUNTY=1.0
```

---

## 📡 JSON-RPC API Examples

**List Deployed System Contracts:**
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":8,"method":"ufi_listContracts","params":{}}'
```

**Inspect Descriptor Bytecode:**
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":10,"method":"eth_getCode","params":["0x102","latest"]}'
```

---

## ⚙️ Operations

**Backup a Data Directory:**
```bash
make backup-datadir DATADIR=./data/local
```

**Run a Build-plus-Backup Rollout:**
```bash
make rollout-node DATADIR=./data/local
```

---

## 🗳️ Governance Flow

**Create Proposal:**
```bash
curl -X POST [http://127.0.0.1:8545/governance/proposals](http://127.0.0.1:8545/governance/proposals) \
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

**Advance Local Governance Height:**
```bash
curl -X POST [http://127.0.0.1:8545/chain/advance](http://127.0.0.1:8545/chain/advance) \
  -H 'Content-Type: application/json' \
  -d '{"blocks":40321}'
```

---

## 🛡️ Launch Readiness & Safety

* **Improved:** System contracts `0x101`/`0x102` now support explicit introspection via `eth_getCode` and `ufi_listContracts`.
* **Improved:** Remote blocks pass validator-quorum checks; nodes persist side branches and handle reorgs via cumulative PoUW work.
* **Still Missing:** Advanced peer reputation and adaptive abuse controls.

---

## ⚖️ License

Licensed under the **GNU General Public License v3.0 (GPL-3.0)**. See LICENSE for details.

**Architect:** [ufi.network](https://ufi.network) | 2026
