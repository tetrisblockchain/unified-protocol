# UniFied Protocol (UFI) 🌐
### The Decentralized Ledger for Global Web Indexing

**UniFied** is a high-performance Layer 1 blockchain prototype designed to decentralize search indexing. By replacing traditional Proof of Work with **Proof of Useful Work (PoUW)**, UniFied transforms energy consumption into a global, verifiable, and uncensorable map of human knowledge.

The current workspace includes a persistent blockchain state engine, a 15-second mining loop, JSON-RPC, governance-aware crawl priority rules, and libp2p GossipSub block propagation.

---

## 🏛️ Core Components

- **`cmd/unified-node`**: Persistent node daemon with BadgerDB-backed chain state, JSON-RPC, governance endpoints, and libp2p networking.
- **`cmd/unified-cli`**: Governance CLI for listing proposals and casting votes.
- **`core/blockchain.go`**: Ledger persistence, transaction/state transitions, search index storage, native `0x101`/`0x102` contract routing, and **3.33% Architect Fee** enforcement.
- **`core/engine.go`**: Multi-threaded mempools plus the PoUW mining loop.
- **`api/rpc_server.go`**: JSON-RPC methods for balances, transfers, blocks, search task submission, local search-index reads, and native contract calls.
- **`contracts/UNS.sol`**: UniFied Name Service registry contract that prices names based on `0x101` search precompile mention frequency.

---

## 💎 Economic Engine: The Architect's Share

To ensure long-term sustainability and continuous innovation, UniFied implements a hard-coded **3.33% Architect Fee** at the consensus level.

* **Sustainability:** 3.33% of every block reward, search bounty, and UNS registration is automatically routed to the **Genesis Architect Address**.
* **Immutability:** This fee is a core state transition rule in `core/blockchain.go`. Any block failing to distribute this share is considered invalid by the network.

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
```

---

## 🏗️ Genesis Bootstrap

The genesis script handles UNS registration, broadcasts the Architect identity, and submits the first crawl seed tasks.

**Single-command bootstrap:**
```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make bootstrap-architect \
  DATADIR=./data/genesis-architect
```

---

## 🔍 Bulk URL Seeding

Seed the node with live governance-aware bounty quotes using a `urls.txt` file (one URL per line).

```bash
ARCHITECT_KEY=<hex-or-base64-ed25519-key> \
make seed-urls \
  URLS_FILE=./urls.txt \
  SEED_QUERY="initial web seed" \
  SEED_BASE_BOUNTY=1.0 \
  SEED_DIFFICULTY=8
```

---

## 📡 JSON-RPC API Examples

**Get a Balance:**
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"ufi_getBalance","params":{"address":"UFI_LOCAL_OPERATOR"}}'
```

**Read Indexed Crawl Data:**
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"ufi_getSearchData","params":{"url":"[https://example.edu](https://example.edu)","term":"search"}}'
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

**Vote via CLI:**
```bash
go run ./cmd/unified-cli vote --proposal 1 --choice Yes
```

---

## 🛡️ Launch Readiness & Safety

* **Syncing:** Peers sync missing blocks over libp2p. Remote blocks pass PoUW validator-quorum checks before import.
* **Persistence:** Governance state and side-branches survive node restarts; reorgs favor the heavier canonical chain.
* **Security:** Ingress paths enforce bounded mempools and RPC/P2P rate limits.

---

## ⚖️ License

Licensed under the **GNU General Public License v3.0 (GPL-3.0)**. See LICENSE for details.

**Architect:** [ufi.network](https://ufi.network) | 2026
