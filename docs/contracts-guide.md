# UniFied Contract System Guide

This guide explains what the UniFied contract system exposes today, how users interact with it, and what the current boundaries are.

## What Exists Today

UniFied currently exposes two classes of contracts:

1. Protocol system contracts
   - `0x101`: search-related native contract surface
   - `0x102`: UNS registry
2. User-deployed contracts
   - `note-v1`: executable minimal user contract
   - `descriptor-v1`: metadata-only user contract descriptor

These are not arbitrary EVM contracts. The node exposes them through contract-style RPC and descriptor bytecode, but execution is still backed by native protocol handlers and the current user-contract runtime.

## What Users Can Do

From the public site:

1. Inspect contracts
   - open the contract registry
   - fetch a contract descriptor
   - inspect functions, selectors, handler type, source, and code hash
2. Run read calls
   - use `eth_call` or `ufi_call`
   - read UNS pricing and mention frequency
   - read `note()` from deployed `note-v1` contracts
3. Send write transactions
   - register UNS names through `0x102`
   - call writable user-contract functions like `setNote(string)`
4. Deploy user contracts
   - deploy `note-v1`
   - deploy `descriptor-v1`
5. Use the public wallet
   - generate or import a session key
   - derive a UFI address locally
   - send UFD
   - resolve and register UNS names
   - inspect address activity

## Public Pages

- `web/site/contracts.html`
  - contract registry
  - contract reads and writes
  - UNS registration
  - user contract deployment
- `web/site/wallet.html`
  - session wallet
  - send and receive
  - UNS name management
  - address activity

## System Contract Surface

### `0x101` Search Contract

Current use:
- mention frequency lookups
- protocol search-related native reads

This is primarily used by the UNS contract for dynamic name pricing.

### `0x102` UNS Registry

Current use:
- quote registration price
- check mention frequency
- register names
- resolve names
- reverse resolve addresses

Typical user flow:

1. Quote the name price
2. Check if the name is already owned
3. Sign a transfer to `0x102` with the exact quoted value
4. Include the `registerName(string)` calldata
5. Submit through `ufi_sendRawTransaction`

## User-Deployed Contract Runtimes

### `note-v1`

Purpose:
- minimal executable public contract owned by a user

Functions:
- `note()`
- `setNote(string)`

Behavior:
- `note()` returns the current stored string
- `setNote(string)` updates that stored string

Use cases:
- public profile notes
- registry annotations
- simple on-chain operator messages

### `descriptor-v1`

Purpose:
- publish a user-owned contract descriptor without executable runtime logic

Use cases:
- reserving a contract identity
- publishing metadata for future tooling
- documenting an external integration surface

## RPC Methods Users Should Know

Read surface:
- `ufi_listContracts`
- `ufi_getContract`
- `eth_getCode`
- `ufi_call`
- `eth_call`
- `ufi_resolveName`
- `ufi_reverseResolve`
- `ufi_getNamePrice`
- `ufi_getAddressActivity`

Write / submission surface:
- `ufi_sendRawTransaction`

## Important Limits

Current limitations:

1. This is not a full arbitrary smart-contract VM.
2. Users cannot deploy arbitrary Solidity/EVM bytecode and expect general execution.
3. The public deployment surface currently targets the built-in UniFied user-contract runtimes.
4. Contract execution is deterministic and protocol-controlled.

That means the current model is closer to:
- protocol-native contracts
- contract-style introspection
- limited deployable user runtimes

Not:
- unrestricted EVM deployment
- arbitrary third-party VM bytecode execution

## Recommended User Onboarding Flow

1. Open the wallet page and create a session wallet
2. Copy the derived UFI address
3. Fund the wallet
4. Register a UNS name
5. Open the contracts page
6. Inspect `0x102`
7. Deploy a `note-v1` contract
8. Read it with `note()`
9. Update it with `setNote(string)`

## Operator Notes

If you expose the public site, proxy these endpoints:

- `/rpc`
- `/ws`
- `/healthz`
- `/p2p/peers`
- `/chain/`
- `/governance/`
- `/consensus/`

Without those, parts of the wallet, contracts page, or explorer will appear degraded or offline.
