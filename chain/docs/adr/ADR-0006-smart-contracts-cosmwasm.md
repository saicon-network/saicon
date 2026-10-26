# ADR-0006: Smart-contract support (CosmWasm)

Status: **Proposed** (2026-06-10)

## Context

SAiCoN today is a pure **native-module** chain (Cosmos SDK / CometBFT): all logic — staking,
slashing, gov, and our PoP modules (validatorregistry, compliance, participation, rewards) — is
Go compiled into the node binary. There is **no general-purpose VM / smart-contract layer**.

That is the right foundation for the protocol's core (per-block hooks, fail-loud invariants,
consensus-level integration). But the ecosystem story — third-party dApps, the AI-compute
**marketplace**, DeFi, and programmable use of `SCN` and Empirical Points — needs
**permissionless programmability** that native modules can't offer (every native change requires
a chain upgrade + governance).

## Decision

Adopt **CosmWasm** (`x/wasm`) as SAiCoN's smart-contract layer:

- WASM-based contract VM as an SDK module, sandboxed and gas-metered.
- Contracts can hold/transfer `SCN`, and **read** PoP state (validator status, Participation
  Score, Empirical Points) through query bindings — enabling EP-/participation-aware dApps and
  the AI-compute marketplace as contracts.
- Native PoP modules stay the source of truth; contracts sit *above* and cannot bypass invariants
  V-1…V-9 or the reward gate.

Smart contracts are **additive** — the core PoP protocol does not depend on them.

## Open questions (to resolve before/at integration — some are 🔒 governance/economic)

1. **Deployment permissioning** — permissionless contract upload, or gov/allow-list gated at
   launch then opened? (security vs openness trade-off). 🔒
2. **Gas & fees** — gas schedule for WASM execution; fee denom (`SCN`); spam/DoS limits.
3. **PoP read bindings** — which participation/compliance state contracts may query, and whether
   any *write* path (e.g. delegation intent) is ever exposed.
4. **Version & supply-chain** — pin `x/wasm` / `wasmvm` versions; audit the integration as part
   of the Q1'27 external audit (the VM is a major security surface).
5. **IBC interplay** — interchain-accounts / contract-to-contract across chains (ties to the IBC
   workstream).

## Consequences

- Unlocks an external developer ecosystem and lets the AI-compute marketplace ship as auditable
  on-chain contracts settling in `SCN`.
- Adds a significant security surface → must be in scope for the external audit before mainnet.
- Increases node binary size and adds a new gas/economic dimension (governed, see open questions).

## Alternatives considered

- **Native-modules-only** (status quo) — rejected for the ecosystem layer: every dApp would need a
  chain upgrade + governance, which doesn't scale to third-party builders.
- **EVM module (`ethermint`/EVM)** — viable alternative VM; deferred in favor of CosmWasm
  (tighter SDK integration, Rust/WASM tooling, native query bindings to our modules). Revisit if
  EVM ecosystem compatibility becomes a priority.
