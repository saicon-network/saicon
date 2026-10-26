---
id: ADR-0001
title: Use Cosmos SDK / CometBFT for SAiCoN L1
status: accepted
date: 2026-05-29
deciders: [human-principal, architect]
context: chain
supersedes: null
superseded_by: null
related: []
---

# ADR-0001: Use Cosmos SDK / CometBFT for SAiCoN L1

## Context

SAiCoN Blockchain — an L1 network with the Proof of Participation (PoP) mechanism. We need a fundamental tech stack for:

1. Consensus engine (BFT-finality, validator set management, block production)
2. Application framework (state machine, modules, msgs, queries, hooks)
3. CLI / RPC layer
4. Ecosystem compatibility (if IBC or exchange integration is needed)

### Constraints

- The team is small
- Bandwidth is limited — no resources to write consensus from scratch
- Custom modules are needed for PoP-specific logic (validatorregistry, participation, compliance, delegation router, rewards)
- The validator system requires non-trivial extensions on top of standard staking

### Forces

- Time-to-localnet is critical — we need an MVP in weeks, not months
- Maintainability — the chain must be upgradable, and ecosystem patches must be easily applicable
- Skill availability — Go + Cosmos SDK is well documented, and agents can work effectively
- Validator subsystem extension — a custom status model must be built on top of the existing x/staking

---

## Decision

**We use Cosmos SDK + CometBFT as the foundation for SAiCoN L1.**

Specifically:
- **CometBFT** — unmodified, for consensus
- **Cosmos SDK** (latest stable) — for the application layer
- **Go** — language for chain code
- Base modules (`x/auth`, `x/bank`, `x/staking`, `x/slashing`, `x/distribution`, `x/gov`, `x/params`, `x/genutil`, `x/crisis`, `x/upgrade`) — out of the box
- 5 custom modules are written as an extension layer (see Chain Architecture)

Scaffold via `ignite chain scaffold` or an equivalent manual setup (WI-001 will decide the exact implementation).

---

## Consequences

### Positive

✅ **Massive time savings** — we do not write consensus, base staking, or gov/slashing/distribution
✅ **Battle-tested** — Cosmos chains are in production at dozens of L1s (Osmosis, Juno, Kava, dYdX, etc.)
✅ **Documentation + community** — an extensive knowledge base; agents work effectively with the Cosmos SDK
✅ **IBC ready** — if bridges are needed in the future, the infrastructure is already in place
✅ **Predictable upgrade path** — the Cosmos SDK upgrade module + x/upgrade coordination
✅ **Tooling ecosystem** — `ignite`, `cosmovisor`, block explorers (Mintscan-compat), wallets (Keplr, Cosmostation)
✅ **Validator-friendly** — existing validator operators know Cosmos chain ops

### Negative

❌ **Lock-in to the Go ecosystem** — it is hard to move to a Rust framework (e.g. Substrate)
❌ **Cosmos SDK breaking changes** — major version bumps (0.50 → 0.55) require migration work
❌ **Module composability constraints** — some Cosmos SDK design decisions impose constraints (e.g. monolithic state machine, no native parallelism for msg execution)
❌ **Performance ceiling** — Tendermint-style BFT does not scale to tens of thousands of TPS; this is not a problem for SAiCoN v0.1, but it is a constraint for the distant future
❌ **Patching required when deviating from mainstream** — custom consensus or novel validator selection would require forking CometBFT

### Neutral / Trade-offs

🔵 **Standard staking remains, extended** — we do not **replace** `x/staking`, we **extend** it via hooks + parallel modules. This preserves the upgrade path but requires careful design so that invariants are not violated.
🔵 **Customization vs maintainability** — every custom module is debt at Cosmos SDK upgrade time; we keep 5 modules, not 50.
🔵 **Reward distribution architecture** — we intercept via the `x/rewards` wrapper and do not modify `x/distribution` directly → cleaner upgrades, slightly more indirection.

---

## Alternatives Considered

### Alt 1: Substrate (Rust)

**Pros:** Modern type system, WASM-based runtime upgrades, parallel execution possibilities, Polkadot ecosystem.

**Cons:**
- Fewer ready-made modules for staking + the validator subsystem
- Rust learning curve for agents (also manageable, but a slower start)
- Parachain model overhead if we go as a parachain; a standalone Substrate chain is more work than Cosmos
- Smaller pool of validator operators who know Substrate ops

**Verdict:** Time-to-MVP is much worse than with Cosmos.

### Alt 2: Custom Build (from scratch)

**Pros:** Full control, optimization for SAiCoN specifics.

**Cons:** Catastrophic timeline blow-up. Consensus correctness is PhD-level work. Validator set management is months of work. Not reasonable for a small team's first network.

**Verdict:** Out of question.

### Alt 3: Avalanche Subnet (Go or AvalancheGo)

**Pros:** High throughput, subnet flexibility, EVM compatibility option.

**Cons:**
- Less mature validator extension primitives for PoP-like custom logic
- Tighter coupling with the Avalanche economic model
- Smaller community for Cosmos-style modular design
- IBC compatibility is not native

**Verdict:** Possible, but Cosmos matches the need for modular validator extension better.

### Alt 4: Polygon CDK / OP Stack (L2 rollup)

**Pros:** Inherits L1 security, fast settlement.

**Cons:**
- SAiCoN was conceived as an L1 with its own validator economy — the rollup model changes the fundamental shape (sequencer vs validators)
- PoP loses its meaning without its own consensus
- Settlement to Ethereum ties us to the ETH gas economy

**Verdict:** Does not fit the PoP network model.

---

## Implementation Notes

### Bootstrap (WI-001)
- Use `ignite chain scaffold` as the starting point OR a manual go.mod + app.go setup
- SCN denom in genesis
- Localnet working — single validator + one test tx via `<chain>d tx bank send`

### Version pinning
- Cosmos SDK version — pinned in `go.mod`
- Upgrade path: via the standard Cosmos upgrade module, coordinated through an ADR at every bump

### Custom module placement
- All custom modules live in `chain/x/<name>/` (standard Cosmos layout)
- Proto definitions in `chain/proto/saicon/<module>/v1/`

### Dependencies management
- New top-level Go dependencies → require review (see Security §4.3)
- `go.sum` always committed

---

## Related Documents

- Chain Architecture — technical module structure
- [Validator System Spec](../validators/README.md) — behavioral contract

External references:
- [Cosmos SDK Docs](https://docs.cosmos.network/)
- [CometBFT Docs](https://docs.cometbft.com/)
- [Ignite CLI](https://docs.ignite.com/)
