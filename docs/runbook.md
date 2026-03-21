# 🌐 UniFied Protocol

<p align="center">
  <img src="Gemini_Generated_Image_htvf05htvf05htvf.svg" alt="UniFied Protocol Logo" width="380px" />
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Network-Mainnet--Genesis-blueviolet?style=for-the-badge&logo=blockchaindotcom" alt="Status" />
  <img src="https://img.shields.io/badge/Language-Go%201.25+-00ADD8?style=for-the-badge&logo=go" alt="Built With" />
  <img src="https://img.shields.io/badge/Architecture-Layer--1%20PoUW-emerald?style=for-the-badge" alt="Architecture" />
  <img src="https://img.shields.io/badge/Security-libp2p%20GossipSub-orange?style=for-the-badge" alt="Networking" />
</p>

---

## 📖 The Vision: Reclaiming the Truth

**UniFied** is an experimental Layer 1 blockchain prototype designed to dismantle the "Search Monopoly." Traditional search engines are centralized, black-boxed, and susceptible to manipulation or corporate bias. 

UniFied introduces **Proof of Useful Work (PoUW)** search indexing. In this ecosystem, mining energy is not spent on arbitrary cryptographic puzzles. Instead, it is directed toward crawling, indexing, and verifying the world's digital knowledge. Every block added to the ledger represents a batch of verifiable web data, "locked" into a permanent public archive that no single entity can hide, delete, or bias.

---

## 🏗️ System Components & Architecture

The workspace is organized into specialized modules handling everything from p2p gossip to governance.

### 📦 Core Infrastructure
* **`cmd/unified-node`**: The persistent node daemon managing BadgerDB, JSON-RPC, and libp2p.
* **`core/blockchain.go`**: The **Heart** of the protocol. Manages ledger persistence and state transitions.
* **`core/engine.go`**: The **Lungs**. Handles transaction mempools and the 15-second PoUW mining loop.
* **`core/system_contracts.go`**: The **Registry**. Definitions for protocol contracts like `0x101` and `0x102`.

---

## 🛠️ Workspace Setup

### 1. Bootstrap Environment
Initialize your local directories and install binary dependencies:
```bash
./setup.sh
```

### 2. Common Make Targets
* `make test` — Run the protocol test suite.
* `make build` — Compile the node and CLI binaries.
* `make run-mine` — Start the node with mining enabled.

---

## 🛰️ Running a Node

To launch a node as an active miner and network operator:
```bash
go run ./cmd/unified-node \
  --network-config ./config/unified-network.json \
  --datadir ./data \
  --rpcport 3337 \
  --mine \
  --operator YOUR_UFI_ADDRESS
```

---

## 📡 JSON-RPC API Guide

### 💰 Check Account Balance
```bash
curl -s -X POST http://localhost:3337/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"ufi_getBalance","params":{"address":"YOUR_ADDRESS"}}'
```

### 🔎 Search the Ledger
```bash
curl -s -X POST http://localhost:3337/rpc \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"ufi_getSearchData","params":{"url":"","term":"architect"}}'
```

---

<p align="center">
  <b>UniFied Protocol Foundation © 2026</b><br/>
  <i>Untangling the Web. Locking the Truth.</i>
</p>
