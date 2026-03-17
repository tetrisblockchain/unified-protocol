# UniFied Protocol (UFI)
### The Decentralized Ledger for Global Web Indexing

**UniFied** is a high-performance Layer 1 blockchain prototype designed to decentralize the act of search indexing. By replacing traditional Proof of Work with **Proof of Useful Work (PoUW)**, UniFied transforms energy consumption into a global, verifiable, and uncensorable map of human knowledge.

The current workspace includes a persistent blockchain state engine, a 15-second mining loop, JSON-RPC, governance-aware crawl priority rules, and libp2p GossipSub block propagation.

---

## Core Components

- **cmd/unified-node**: Persistent node daemon with BadgerDB-backed chain state, JSON-RPC, governance endpoints, and libp2p networking.
- **cmd/unified-cli**: Governance CLI for listing proposals and casting votes.
- **core/blockchain.go**: Ledger persistence, transaction/state transitions, search index storage, native 0x101/0x102 contract routing, and **3.33% Architect Fee** enforcement.
- **core/engine.go**: Multi-threaded mempools plus the PoUW mining loop.
- **api/rpc_server.go**: JSON-RPC methods for balances, transfers, blocks, search task submission, local search-index reads, and native contract calls.
- **contracts/UNS.sol**: UniFied Name Service registry contract that prices names based on 0x101 search precompile mention frequency.

---

## Economic Engine: The Architect's Share

To ensure long-term sustainability and continuous innovation, UniFied implements a hard-coded **3.33% Architect Fee** at the consensus level.

* **Sustainability:** 3.33% of every block reward, search bounty, and UNS registration is automatically routed to the **Genesis Architect Address**.
* **Immutability:** This fee is a core state transition rule in core/blockchain.go. Any block failing to distribute this share is considered invalid by the network.

---

## Workspace Setup

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

## Running The Node

To start a local instance with a custom data directory and mining enabled:

```bash
./build/unified-node \
  --datadir ./data \
  --rpcport 8545 \
  --mine \
  --bootnodes /ip4/203.0.113.10/tcp/4001/p2p/12D3KooW... \
  --genesis-address UFI_SHARED_GENESIS_FUNDER \
  --operator UFI_LOCAL_OPERATOR \
  --operator-alias local-operator \
  --circulating-supply 1000000
```

> **Note:** For multi-node operation, every peer must share the same --genesis-address and --circulating-supply values so the genesis block hash and state root match across the network.

---

## JSON-RPC API Examples

### Get a Balance:
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"ufi_getBalance","params":{"address":"UFI_LOCAL_OPERATOR"}}'
```

### Read Indexed Crawl Data:
```bash
curl -s -X POST [http://127.0.0.1:8545/rpc](http://127.0.0.1:8545/rpc) \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"ufi_getSearchData","params":{"url":"[https://example.edu](https://example.edu)","term":"search"}}'
```

---

## Governance Flow

UniFied Governance (UGF) allows the community to decide "Search Priorities" via executable code.

### 1. Create Proposal:
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

### 2. Vote via CLI:
```bash
./build/unified-cli vote --proposal 1 --choice Yes
```

---

## License

Licensed under the **GNU General Public License v3.0 (GPL-3.0)**. See LICENSE for details.

**Architect:** ufi.network | 2026
