# ADR-0015: The sentry layout — retention and exposure are two different axes

Status: **Proposed** (2026-08-27) — target slot S4 (27 Aug – 9 Sep 2026), adopted before
S9 opens the DevNet to invited operators. See [DEVELOPMENT.md](../DEVELOPMENT.md) §2.10.2.
Related: [ADR-0004](ADR-0004-validator-activation-model.md) (what a validator is),
§2.10.1 (data retention, the other axis), and the S9 invite, whose operator instructions
this decides.

## Context

§2.10.2 records the five node roles and their two axes. **Retention** — how much a node
keeps — now has three profiles in the tree: `validator`, `full` and `archive`.
**Exposure** — what a node faces — has none. Every node today runs CometBFT's
`pex = true` with an empty `private_peer_ids`, which is the SDK default and entirely
appropriate for a single-operator DevNet.

It stops being appropriate the moment two things are true at once, and S4 makes both
true: the network has more than one node, and its endpoints are public.

The problem is not storage. A validator's job is to sign blocks, and public query load on
a signing node is an availability question independent of what that node stores. An
explorer that gets popular, or one bot in a loop, otherwise reaches the same machine that
produces blocks. Worse, `pex = true` gossips the validator's address to every peer that
asks, so an attacker does not need to find the validator — the network tells them.

Nothing here is ours to invent. This is the Cosmos Sentry Node Architecture, and
Ethereum and Polkadot arrive at the same shape from the same pressure.

## Decision

### D1 — The signing node never faces the open internet

A validator runs `pex = false` with `persistent_peers` pointing **only** at its own
sentries. It accepts no inbound connections from anywhere else. It is reachable, in the
network's view, exclusively through nodes it owns.

### D2 — Sentries carry the exposure, and hide the validator's address

A sentry runs `pex = true` so it participates normally in peer exchange, and lists the
validator in `private_peer_ids`. That flag is what stops the validator's address being
gossiped: a private peer is used but never advertised. A sentry that omits it defeats the
entire layout while looking correct.

### D3 — Two sentries minimum, in different regions and at different providers

One sentry is a single point of failure with extra steps: if it goes down the validator
is partitioned from the network and stops signing, which is the outcome the layout exists
to prevent. Two is the floor. Different regions and different providers, or a single
provider incident takes both.

### D4 — A sentry takes the `full` retention profile

Retention and exposure are separate axes, so this is a choice rather than a consequence.
A sentry serves p2p and, where it fronts public RPC, query traffic — both of which need
blocks far more than historical state. `full` is the profile that matches: every block,
state pruned to the validator's window. An archive sentry would pay for history nobody
asks a sentry for.

### D5 — Public RPC and REST terminate on a sentry or a `full` node, never on the validator

The explorer, the application backend and any published endpoint point at a non-signing
node. This is what makes D1 hold in practice rather than only in the peer configuration:
a validator with a public RPC port is exposed no matter what `pex` says.

### D6 — What this ADR does not decide

- **The seed role.** §2.10.2 records it as not required yet; nothing here changes that.
- **Rate limiting.** It belongs at the reverse proxy and is named in the DevOps runbook.
  A sentry layout without a limiter still lets one client exhaust a sentry.
- **How many sentries a validator must run at MainNet**, which is an operator-program
  question rather than an architectural one.

## Consequences

**An invited operator is told to run more than one machine.** That is the real cost, and
it is why this lands before S9 rather than inside it: the invitation, the join guide and
the hardware guidance all change. A single-box operator can still join a test network —
the layout is a recommendation there and an expectation at MainNet.

**Our own DevNet gains two node roles it did not have.** The restart owed since July is
the moment to adopt the shape rather than retrofit it.

**The validator's liveness now depends on its sentries.** This is the trade being made:
the attack surface shrinks and a new partition risk appears in its place, which D3
answers by requiring two independent ones.

## Alternatives considered

**Leave `pex = true` and rely on a firewall.** A firewall filters inbound connections but
does not stop the address being gossiped, and an address that is public is a target
whether or not the port answers today.

**Put the validator behind a load balancer instead of sentries.** A load balancer
terminates client traffic; consensus p2p is not client traffic, and the validator still
needs peers. It solves the RPC half of D5 and none of D1.

**Defer until S9, when operators are invited.** Rejected: the layout changes what the
invitation says, so it has to exist before the invitation is written. §2.10.2 already
ranks it above the middle retention profile for this reason.

## References

- §2.10.2 — node roles, the two axes, and where SAiCoN sits in the standard vocabulary
- §2.10.1 — the retention policy, and the profiles this composes with
- The devnet operations runbook (task 5) — public endpoints and the
  rate limiting D6 leaves to it
