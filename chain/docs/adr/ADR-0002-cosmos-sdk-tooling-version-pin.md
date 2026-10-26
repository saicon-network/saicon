---
id: ADR-0002
title: Cosmos SDK and tooling version pin (v0.53.6, Ignite v29.10.0, Go 1.25.10)
status: accepted
date: 2026-05-29
deciders: [human-principal, builder]
context: chain
supersedes: null
superseded_by: null
related: [ADR-0001]
---

# ADR-0002: Cosmos SDK and tooling version pin

> **Updated 2026-05-31 (WI-D): reconciled Go version to the `go.mod` toolchain (`go 1.25.10`).** `go.mod` is the single source of truth for the Go toolchain — it drives Docker (`golang:1.25-alpine`) and CI (`setup-go` with `go-version-file: chain/go.mod`). The `1.26.3` referenced in the original WI-001 record was only the developer's locally-installed Go at scaffold time; Go 1.26.3 is backward-compatible and builds a `go 1.25` module fine, but it is **not** the declared toolchain.

## Context

[ADR-0001](ADR-0001-use-cosmos-sdk.md) recorded the choice of Cosmos SDK as the foundation. While executing WI-001 (Bootstrap Cosmos SDK scaffold) we need to pin the **exact versions** of the SDK, the scaffold tool and the Go runtime, so that:

1. Future work items have a predictable environment
2. Upgrading the Cosmos SDK is a deliberate decision (not accidental drift from `latest`)
3. CI and developer machines run on the same versions

### Forces

- We want a recent LTS series, but one proven by the ecosystem
- We do not want bleeding edge — this is the team's first Cosmos chain, and tooling stability is needed
- We want good support for the latest Cosmos features (the new modules architecture, IBC v8+)
- Tooling must be available through canonical package managers (brew/Go), without curl-to-bash

---

## Decision

**Pinned versions for WI-001 and initial work:**

| Component | Version | Source |
|---|---|---|
| Cosmos SDK | **v0.53.6** (Eden++ series) | go.mod pinned |
| CometBFT | (whatever ships with SDK v0.53.6) | transitive |
| Ignite CLI | **v29.10.0** | brew (Homebrew-core formula) |
| Go toolchain | **1.25.10** (per `go.mod`, authoritative) | `go.mod` `go` directive → Docker `golang:1.25-alpine` + CI `go-version-file` |

### Concrete scaffold parameters

The scaffold was run with:

```bash
ignite scaffold chain github.com/saicon-network/saicon/chain \
  --address-prefix saicon \
  --default-denom scn \
  --no-module \
  --skip-git
```

- Module path: `github.com/saicon-network/saicon/chain` (matches the filesystem layout)
- Binary name: `saicond` (after `mv cmd/chaind cmd/saicond` + sed rename)
- App.Name constant: `"saicon"` (after sed)
- Bech32 address prefix: `saicon` (e.g. `saicon1dvcr36u8hgc9kk9f72c6xjx9f37etk20srvr43`)
- Default denom: `scn`
- DefaultNodeHome: `~/.saicon` (our binary), but ignite chain serve uses `~/.chain` (legacy)

### What we do NOT use

- ❌ `curl https://get.ignite.com/cli! | bash` — supply chain risk
- ❌ `go install github.com/ignite/cli/v28/ignite@latest` — tried, the path does not work in v28 (the module exists, the package does not)
- ❌ Manual `simapp` based scaffold — slower, no benefit for v0.1

---

## Consequences

### Positive

✅ **Latest LTS Cosmos SDK** — v0.53 series, Eden++ improvements (collections, AppModule v2, autocli)
✅ **Brew-installable Ignite** — verifiable bottle, easy upgrade/downgrade through brew
✅ **No external curl-bash** — complies with Security & Governance §7
✅ **Reproducible env** — pinned versions fix a reference point for all future WIs

### Negative

❌ **Ignite v29 vs targeted v28** — we planned v28 (stable with SDK v0.50), but the brew formula pulls v29 (with SDK v0.53). For the v0.1 chain this is not a problem (newer = more features), but the additional breaking changes between SDK 0.50→0.53 (especially in staking, distribution, x/gov) must be taken into account in custom modules.
❌ **v0.53 is less tested by the ecosystem** than v0.50. Production Cosmos chains are mostly on v0.47/0.50. If auditing/standardization is needed in the future, a downgrade is worth considering.
❌ **Go toolchain 1.25.10** — a relatively fresh series; golangci-lint does not fully track it yet (hence `lint.yml` is advisory / `continue-on-error`). Build/test/vet all work. (The WI-001 dev machine had Go 1.26.3 installed locally — backward-compatible, it builds a `go 1.25` module without problems — but it is not the declared toolchain.)

  *Update 2026-10-26:* golangci-lint v2.12.2 tracks the toolchain; `lint.yml` is blocking, on `chain/` and `indexer/`, with zero findings.

### Neutral

🔵 **Ignite chain serve uses the `~/.chain` home** instead of our `~/.saicon` — legacy behavior, does not block development. All custom tests use `--home ~/.chain`. We will switch explicitly in the production setup.
🔵 **The saicond binary is available in `~/go/bin/`** (after `go install ./cmd/saicond`). Alternatively, it is built via `make build`.

---

## Alternatives Considered

### Alt 1: Cosmos SDK v0.50.x (Eden) + Ignite v28

**Pros:** Battle-tested, used by majority of production Cosmos chains (Osmosis, Juno, etc.), abundant docs.

**Cons:**
- Ignite v28 cannot be installed via `go install` (broken main package path)
- Via `curl ... | bash` — supply chain risk
- The brew formula is already on v29; a downgrade requires a manual tap or version locking

**Verdict:** v0.50 is theoretically ideal, but tooling friction outweighs it in Phase 0/1. We can consider a downgrade in Phase 4+ if custom modules require v0.50-specific patches.

### Alt 2: Cosmos SDK v0.47 (LTS, older)

**Pros:** Very stable, IBC v7 is well documented, broad validator operator familiarity.

**Cons:** Old — lacks the collections framework, autocli and other v0.50+ features. It will be deprecated upstream in 1-2 years.

**Verdict:** Too old for a new chain in 2026.

### Alt 3: Latest unstable (v0.54-rc / main)

**Verdict:** No. Unconditionally no. No "main" in dependencies.

---

## Implementation Notes

### Verification (done during WI-001 execute)

Declared toolchain (authoritative): `go.mod` → `go 1.25.10`. CI (`setup-go` with `go-version-file: chain/go.mod`) and Docker (`golang:1.25-alpine`) both build against this.

```
go.mod go directive                                          → go 1.25.10  (source of truth)
go version (dev machine at WI-001 scaffold)                  → go1.26.3 darwin/arm64 (compatible, not the declared toolchain)
ignite version                                               → v29.10.0-dev (Cosmos SDK v0.53.6)
go build ./...                                               → success
go install ./cmd/saicond                                      → ~/go/bin/saicond (152MB)
ignite chain serve --reset-once                              → blocks producing
saicond query bank total-supply --home ~/.chain               → 300000186 scn ✓
saicond tx bank send alice bob 100scn (with fees 200scn)     → height 275, code 0 ✓
```

### Future upgrade procedure

When we want to bump Cosmos SDK / Ignite:
1. Create a new ADR (ADR-NNNN) with the rationale
2. Update go.mod `cosmossdk.io/...` versions
3. `go mod tidy`
4. Re-run full build + localnet smoke test
5. Update this ADR with `superseded_by: ADR-NNNN`

### Replace directives

go.mod contains a `replace` block for compatibility with sub-dependencies (a standard Cosmos SDK pattern). Do not change without justification.

---

## Related Documents

- [ADR-0001 Use Cosmos SDK](ADR-0001-use-cosmos-sdk.md) — the overall stack choice
- Chain Architecture — module structure (custom modules on the v0.53 API)
- Security & Governance §7 — supply chain rules
