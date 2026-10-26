---
id: ADR-0003
title: Validator status enum normalization
status: accepted
date: 2026-05-31
deciders: [project team]
context: chain
supersedes: null
superseded_by: null
related: [ADR-0001, ADR-0002]
---

# ADR-0003: Validator Status Enum Normalization

## Context

[Validator System Spec v0.1](../validators/README.md) §2 defines 6 validator statuses (`bonding`, `active`, `quarantined`, `jailed`, `unbonding`, `inactive`), but does not pin down:

1. Go type representation
2. Proto encoding for wire/storage
3. Mapping to Cosmos SDK `stakingtypes.BondStatus` (`Unbonded` / `Unbonding` / `Bonded`)
4. Ordering for index purposes
5. Stringer / parsing semantics

This was the critical finding of the WI-002 review. Without this decision WI-003 (x/validatorregistry storage skeleton) cannot start — a proto type for ValidatorMetadata is needed.

### Forces

- Cosmos SDK convention: proto enums with `_UNSPECIFIED = 0` (proto3 default safety)
- Need: fast equality check, ordering for diagnostic queries, stable wire encoding
- Need: clear mapping to the existing BondStatus (for x/staking integration)
- Want: human-readable names in CLI and API responses
- Want: extensibility (if we later add, e.g., a `migrating` status)

---

## Decision

### Go type

```go
// chain/x/validatorregistry/types/status.go

type Status int32

const (
    Status_STATUS_UNSPECIFIED Status = 0  // reserved, an error if encountered at runtime
    Status_STATUS_BONDING     Status = 1
    Status_STATUS_ACTIVE      Status = 2
    Status_STATUS_QUARANTINED Status = 3
    Status_STATUS_JAILED      Status = 4
    Status_STATUS_UNBONDING   Status = 5
    Status_STATUS_INACTIVE    Status = 6
)
```

The numeric values are **pinned** and **must not change** (wire compatibility).

### Proto definition

```protobuf
// chain/proto/saicon/validatorregistry/v1/status.proto

syntax = "proto3";

package saicon.validatorregistry.v1;

option go_package = "github.com/saicon-network/saicon/chain/x/validatorregistry/types";

enum Status {
  STATUS_UNSPECIFIED = 0;
  STATUS_BONDING     = 1;
  STATUS_ACTIVE      = 2;
  STATUS_QUARANTINED = 3;
  STATUS_JAILED      = 4;
  STATUS_UNBONDING   = 5;
  STATUS_INACTIVE    = 6;
}
```

Cosmos SDK proto convention: enum name = `Status`, values prefixed `STATUS_` (matches the `BondStatus` pattern).

### String representation

```go
func (s Status) String() string {
    switch s {
    case Status_STATUS_BONDING:     return "bonding"
    case Status_STATUS_ACTIVE:      return "active"
    case Status_STATUS_QUARANTINED: return "quarantined"
    case Status_STATUS_JAILED:      return "jailed"
    case Status_STATUS_UNBONDING:   return "unbonding"
    case Status_STATUS_INACTIVE:    return "inactive"
    default:                        return "unknown"
    }
}

func StatusFromString(s string) (Status, error) {
    switch s {
    case "bonding":     return Status_STATUS_BONDING, nil
    case "active":      return Status_STATUS_ACTIVE, nil
    case "quarantined": return Status_STATUS_QUARANTINED, nil
    case "jailed":      return Status_STATUS_JAILED, nil
    case "unbonding":   return Status_STATUS_UNBONDING, nil
    case "inactive":    return Status_STATUS_INACTIVE, nil
    default:            return Status_STATUS_UNSPECIFIED, fmt.Errorf("unknown status: %q", s)
    }
}
```

CLI and API use a **lowercase string** (`"active"`). Storage and wire use int32. JSON marshaling uses a string (via MarshalJSON / UnmarshalJSON).

### Mapping to Cosmos SDK BondStatus

`stakingtypes.BondStatus` has 3 values: `Unbonded`, `Unbonding`, `Bonded`. The mapping is many-to-one (SAiCoN statuses → BondStatus):

| SAiCoN Status | Cosmos BondStatus | Reasoning |
|---|---|---|
| `STATUS_BONDING` | `Unbonded` | Lock-up is active but the validator is not yet in the active set |
| `STATUS_ACTIVE` | `Bonded` | In the active set, signing blocks |
| `STATUS_QUARANTINED` | `Unbonded` | Does not participate in consensus, but the stake is locked |
| `STATUS_JAILED` | `Unbonded` | Cosmos SDK convention: jailed = unbonded |
| `STATUS_UNBONDING` | `Unbonding` | Cooldown is active |
| `STATUS_INACTIVE` | `Unbonded` | Stake released, the validator may be removed |

Mapping function:

```go
func (s Status) ToBondStatus() stakingtypes.BondStatus {
    switch s {
    case Status_STATUS_ACTIVE:
        return stakingtypes.Bonded
    case Status_STATUS_UNBONDING:
        return stakingtypes.Unbonding
    case Status_STATUS_BONDING, Status_STATUS_QUARANTINED, Status_STATUS_JAILED, Status_STATUS_INACTIVE:
        return stakingtypes.Unbonded
    default:
        return stakingtypes.Unspecified
    }
}
```

**Reverse mapping is impossible** (lossy): the SAiCoN status cannot be recovered from `BondStatus`. This is by design — the SAiCoN status is stored in `x/validatorregistry`, and BondStatus is a derived view for x/staking integration.

### Ordering

The numeric values do **not** carry lifecycle ordering (i.e. `BONDING < ACTIVE` ≠ "active is more advanced than bonding"). The numbers exist only for proto wire stability.

If a diagnostic ordering is needed for CLI display:
```go
var StatusDisplayOrder = []Status{
    Status_STATUS_ACTIVE,
    Status_STATUS_BONDING,
    Status_STATUS_UNBONDING,
    Status_STATUS_QUARANTINED,
    Status_STATUS_JAILED,
    Status_STATUS_INACTIVE,
}
```

Used in `keys list --sort-by-status`.

### Validation

```go
func (s Status) IsValid() bool {
    return s >= Status_STATUS_BONDING && s <= Status_STATUS_INACTIVE
}

func (s Status) IsActive() bool {
    return s == Status_STATUS_ACTIVE
}

func (s Status) IsRewardEligible() bool {
    // This is only the status part. The full gate — see x/rewards.
    return s == Status_STATUS_ACTIVE
}

func (s Status) CanReceiveDelegations() bool {
    return s == Status_STATUS_ACTIVE
}
```

---

## Consequences

### Positive

✅ **Wire stability** — numeric values are pinned and will not break existing state on a module upgrade
✅ **Proto3 safety** — UNSPECIFIED=0 catches uninitialized errors
✅ **Cosmos convention** — the `STATUS_*` format matches the `BondStatus` pattern and is easy to read
✅ **Clear staking mapping** — a clear many-to-one function, not ambiguous
✅ **CLI friendly** — lowercase strings for humans, int32 for machines
✅ **Extension safe** — new statuses are added via numbers 7+, breaking nothing

### Negative

❌ **Mapping lossy** — the SAiCoN status cannot be recovered from BondStatus → we must rely on our own storage
❌ **Six statuses** — a larger state space than the Cosmos standard (3) → more transition tests are needed
❌ **No lifecycle ordering in the enum** — a separate `StatusDisplayOrder` slice is needed for the UI

### Neutral / Trade-offs

🔵 **No `migrating` status** — if upgrade migrations with a temporary state appear in the future, it will need to be added (number 7). That is fine; the design accommodates it.
🔵 **`quarantined` mapped to `Unbonded` not `Unbonding`** — because the stake stays locked but the validator is not in consensus. `Unbonding` is reserved for an active withdrawal cooldown.

---

## Alternatives Considered

### Alt 1: String enum (status="active")

**Pros:** Human-readable in storage, easy debugging
**Cons:** ~10× larger storage size; no proto3 safety on uninitialized values; a rename breaks state
**Verdict:** No.

### Alt 2: Bool flags (active bool, jailed bool, quarantined bool, ...)

**Pros:** Independent toggling, no state machine
**Cons:** Inconsistent states are possible (active=true AND jailed=true), invariant V-1 loses its meaning, query complexity
**Verdict:** No.

### Alt 3: Inherit Cosmos `BondStatus` directly

**Pros:** No new enum, automatic x/staking compat
**Cons:** Only 3 values — it is impossible to distinguish `bonding` vs `quarantined` vs `jailed` vs `inactive`. This compresses the entire PoP design.
**Verdict:** No — we need 6 distinct statuses.

### Alt 4: Two-tier status (BondStatus + SaiconSubstatus)

**Pros:** Cleanly separates Cosmos-native vs SAiCoN-specific
**Cons:** Every query requires 2 fields; more invariants for consistency; harder for CLI display
**Verdict:** Worth considering for future modules, but overkill for v0.1. For now a single Status with a derived BondStatus is sufficient.

---

## Implementation Notes

### WI-003 deliverables (now unblocked)

- `chain/proto/saicon/validatorregistry/v1/status.proto` — proto enum
- `chain/proto/saicon/validatorregistry/v1/types.proto` — `ValidatorMetadata` uses `Status`
- `chain/x/validatorregistry/types/status.go` — Go helpers (String, FromString, ToBondStatus, IsValid, etc.)
- Unit tests for all mapping functions

### Tests required

1. Round-trip `Status → string → Status` for every valid value
2. `StatusFromString` rejects invalid strings
3. `ToBondStatus` for all 6 statuses + Unspecified
4. `IsValid` for valid + invalid (-1, 7, 999)
5. JSON marshal/unmarshal (string form)

### Migration safety

This ADR is the **first** definition of Status. If a change is ever needed:
- ❌ Never reuse numeric values
- ❌ Never rename existing statuses
- ✅ Add new statuses via numbers 7+
- ✅ Deprecating: keep the numbers, update String() to e.g. "active_deprecated"

---

## Related Documents

- [Validator System Spec](../validators/README.md) — §2 (now normalized)
- Chain Architecture — module dependency graph
- [ADR-0001 Use Cosmos SDK](ADR-0001-use-cosmos-sdk.md)
- [ADR-0002 Tooling version pin](ADR-0002-cosmos-sdk-tooling-version-pin.md)
