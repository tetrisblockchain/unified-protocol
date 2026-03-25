# UFI MinerBox And Scale Roadmap

This document defines two things:

1. the custom hardware path for a dedicated UFI mining appliance
2. the software work required for the protocol to sustain rapid growth without self-bottlenecking

The current protocol is not a Bitcoin-style hash race. In this repository, useful work is dominated by:

- HTTP fetch and crawl execution in `core/consensus/pouw.go`
- task scheduling and block production in `core/engine.go`
- proof validation during block import in `core/block_validation.go`
- crawl proof storage, autonomous frontier spawning, and state persistence in `core/blockchain.go`

## Design Goals

- Increase useful crawl throughput without rewarding wasted duplicate work.
- Keep import/validation cost from growing faster than mined work.
- Prevent one slow domain, one slow sender, or one oversized proof from stalling the network.
- Build a hardware path that aligns with real hot paths instead of generic crypto-mining assumptions.

## MinerBox Hardware Roadmap

## v1: Production Crawl Server

This is the first machine to build and deploy. It should be a standard server, not custom silicon.

- CPU: 16-32 high-clock server cores
- RAM: 128-256 GB ECC
- Storage: 2-4 enterprise NVMe drives
- Network: dual 25 GbE or better
- Form factor: 1U or 2U server
- OS target: Ubuntu 22.04 LTS or newer

Why this works:

- current mining is fetch/parse/hash/store heavy, not GPU heavy
- proof quality depends on page content, term counts, and body size
- the node currently spends real time on network IO, HTML extraction, hashing, and Badger persistence

## v2: Accelerated Appliance

This keeps a server CPU but adds dedicated acceleration cards.

- DPU or SmartNIC for TLS, packet steering, DNS/TCP session handling, and network isolation
- FPGA card for deterministic text-processing kernels
- NVMe-backed scratch tier for temporary crawl buffers and proof construction
- simple microcontroller only for board management, power sequencing, watchdogs, and telemetry

Recommended acceleration targets:

- HTML normalization
- outbound link extraction
- SHA-256 content hashing
- SimHash
- term counting
- compression and chunking
- dedupe filters

Arduino-class boards are acceptable only for management-side tasks such as LEDs, watchdogs, fan curves, relays, and thermal policies. They are not suitable as mining compute devices.

## v3: Custom Motherboard

Only start this after the software pipeline is stable.

- server CPU socket or soldered server SoC
- ECC memory topology
- 4x NVMe or more
- 25/100 GbE onboard or via mezzanine
- one DPU slot
- one FPGA slot
- onboard management MCU/BMC

This board should be built for crawl throughput, storage bandwidth, and network concurrency, not for brute-force integer pipelines.

## v4: ASIC

Do not tape out an ASIC until the proof pipeline is frozen.

ASIC-worthy kernels:

- text canonicalization
- content hashing
- similarity primitives
- tokenization and term counting
- compression

Do not ASIC-encode:

- crawl policies
- governance multipliers
- peer-to-peer logic
- frontier scheduling rules
- anything that is still changing release to release

## Scale Checklist

These are the five missing steps required for sustained growth. Each one includes the current bottleneck, code targets, and the acceptance condition.

## 1. Parallel Miner Workers

Status: completed

Current bottleneck:

- `core/engine.go` mines search tasks serially
- each mining tick drains only 8 mempool tasks
- the default mining interval is 15 seconds

Code targets:

- `core/engine.go`
- `cmd/unified-node/main.go`
- `core/engine_pool_test.go`
- `README.md`
- `docs/runbook.md`

Changes:

- add configurable search-task worker concurrency
- add configurable mempool task batch size
- add configurable frontier task batch size
- keep deterministic block assembly rules while parallelizing crawl execution
- preserve sender nonce ordering and quarantine behavior

Suggested config surface:

- `UNIFIED_MINE_CONCURRENCY`
- `UNIFIED_MINE_MEMPOOL_BATCH`
- `UNIFIED_MINE_FRONTIER_BATCH`
- `UNIFIED_MINE_TASK_TIMEOUT`

Acceptance:

- one mining tick can process more than 8 tasks
- workers run concurrently without violating sender ordering or corrupting block assembly
- tests prove requeue and quarantine behavior still works under concurrency

## 2. Shared HTTP Transport And Host Concurrency Control

Status: completed

Current bottleneck:

- `core/consensus/pouw.go` creates crawler state per task
- transport currently disables keep-alives
- there is no explicit host-level concurrency or backoff policy

Code targets:

- `core/consensus/pouw.go`
- `cmd/unified-node/main.go`
- `core/engine_pool_test.go`
- new crawler-focused tests if needed

Changes:

- create a reusable crawler transport instead of per-task transport churn
- enable keep-alives and connection reuse
- add per-host concurrency ceilings
- add host-level retry and cooldown policy
- add crawler settings for total parallelism, per-host parallelism, idle conns, and body limits

Suggested config surface:

- `UNIFIED_CRAWLER_TIMEOUT`
- `UNIFIED_CRAWLER_MAX_BODY_BYTES`
- `UNIFIED_CRAWLER_PARALLELISM`
- `UNIFIED_CRAWLER_PER_HOST`
- `UNIFIED_CRAWLER_IDLE_CONNS`
- `UNIFIED_CRAWLER_IDLE_CONNS_PER_HOST`

Acceptance:

- connection reuse is visible in transport config
- one slow or hostile domain no longer degrades whole-node crawl throughput
- same-host crawls are bounded instead of unbounded or purely serial

## 3. Reduce Validation Amplification

Status: completed

Current bottleneck:

- `core/block_validation.go` replays crawl validation across 3 local validators for imported proofs
- at larger scale this multiplies crawl load during block import

Code targets:

- `core/block_validation.go`
- `core/consensus/pouw.go`
- `cmd/unified-node/main.go`
- validation and import tests

Changes:

- add validation mode settings: strict, sampled, opportunistic
- allow sampled proof verification instead of validating every imported proof the same way
- add deterministic sampling controls
- prepare for witness-based or challenge-based validation later

Suggested config surface:

- `UNIFIED_VALIDATION_MODE`
- `UNIFIED_VALIDATION_SAMPLE_SIZE`
- `UNIFIED_VALIDATION_MAX_PROOFS_PER_BLOCK`
- `UNIFIED_VALIDATION_TIMEOUT`

Acceptance:

- block import cost does not scale linearly with all proof count at high load
- heavy proof blocks remain importable without forcing full recrawl of every proof

## 4. Shrink Proof And Storage Pressure

Status: completed

Current bottleneck:

- proof payloads can still carry large page bodies
- large proof bodies increase block size, import time, and storage pressure
- search/state persistence in `core/blockchain.go` remains a long-term scaling boundary

Code targets:

- `core/blockchain.go`
- `core/block_validation.go`
- `api/rpc_server.go`
- `web/desktop`
- `web/site`
- storage-related tests

Changes:

- add configurable maximum retained body size for stored proofs
- preserve hashes, snippet, title, and metadata while trimming full stored bodies
- optionally store chunked or externalized payload references instead of raw bodies
- make search/explorer endpoints explicit about trimmed bodies

Suggested config surface:

- `UNIFIED_PROOF_MAX_BODY_BYTES`
- `UNIFIED_PROOF_STORE_FULL_BODY`
- `UNIFIED_PROOF_SNIPPET_BYTES`

Acceptance:

- large crawls do not cause runaway block or snapshot growth
- explorer/search still work with trimmed bodies
- proof integrity is preserved even when the full body is not retained inline

## 5. Task Leasing, Partitioning, And Observability

Status: completed

Current bottleneck:

- miners can still race on the same available work
- there is limited visibility into throughput, lag, host failures, and validation overhead

Code targets:

- `core/engine.go`
- `core/p2p.go`
- `api/rpc_server.go`
- `cmd/unified-node/main.go`
- `web/site/assets/dashboard.js`
- `web/desktop/src/App.tsx`

Changes:

- add deterministic peer-based task partitioning for mempool and frontier work
- requeue drained but unowned mempool tasks immediately so they stay available to the owning miner
- expose mining ownership state through `ufi_getMiningStatus`
- surface partitioning and frontier ownership in the desktop mempool page and the public dashboard
- extend `ufi_getMempoolStatus` with frontier, quarantine, and last-tick ownership counters

Suggested config surface:

- `UNIFIED_MINE_PARTITIONING`

Acceptance:

- connected miners deterministically split crawl work across the current peer set
- unowned mempool tasks remain pending instead of being dropped by non-owning miners
- operators can inspect partition ownership and frontier saturation through RPC and UI surfaces

## Execution Order

Work these in order:

1. parallel miner workers
2. shared crawler transport and host controls
3. validation amplification reduction
4. proof/storage trimming
5. leasing and observability

This order matters because:

- steps 1 and 2 unlock immediate crawl throughput
- step 3 prevents import cost from exploding as throughput rises
- step 4 prevents storage from becoming the next hard wall
- step 5 improves network-wide efficiency and operator visibility

## First Implementation Milestone

Milestone 1 should include only the highest-ROI changes:

- configurable mining worker pool
- configurable mining task batch sizes
- shared keep-alive crawler transport
- per-host crawler concurrency controls

Files expected to change first:

- `core/engine.go`
- `core/consensus/pouw.go`
- `cmd/unified-node/main.go`
- `core/engine_pool_test.go`

## Success Metrics

Track these once observability lands:

- search tasks mined per minute
- frontier tasks mined per minute
- average crawl latency
- 95th percentile crawl latency
- average block import latency
- validator recrawl count per imported block
- average proof payload size
- average stored body size
- duplicate work ratio across miners

## Non-Goals For Now

- general-purpose EVM ASIC work
- GPU-first mining paths
- custom silicon before proof semantics stabilize
- consumer microcontroller-based mining hardware
