# ADR-0004: Validator Activation Model (staking-hook driven)

Status: Accepted (2026-06-09)

## Context
At v0.1.0 nothing moved a validator from BONDING to ACTIVE, blocking the live
positive-reward Proof-of-Participation cycle. The §2.2 transition table also
contradicted §3 on whether the self-stake lock-up gates activation or unbonding.

## Decision
- Activation is driven by the x/staking `AfterValidatorBonded` hook, owned by
  x/validatorregistry and provided via depinject `StakingHooksWrapper`
  (collected alongside x/compliance into MultiStakingHooks).
- `ActivateOnBond` behaviour: registered BONDING -> ACTIVE; unknown validator
  -> auto-create (BONDING) then ACTIVE (covers genesis validators that never
  call MsgRegisterValidator); already ACTIVE -> no-op; JAILED / QUARANTINED /
  UNBONDING / INACTIVE -> skip (a bond event must not reactivate a penalised or
  exited validator; recovery uses the governed compliance/unjail path).
- Transitions go through Keeper.ChangeStatus so the V-1 gate validates the edge
  and the status-changed event is emitted. The auto-create path also emits the
  "none -> BONDING" birth event so genesis validators are traceable.
- The self-stake lock-up gates **unbonding**, not activation. §2.2 wording corrected
  to match §3.

## Consequences
- Genesis validators become ACTIVE automatically; the EP-gated reward path is now
  demonstrable end-to-end.
- Recovery from JAILED/QUARANTINED stays on the governed compliance/unjail path.
- The delegation-router enforcement ADR previously sketched as "ADR-0004" is
  renumbered to ADR-0005 (roadmap M2-A).

## Alternatives considered
- EndBlock scan to promote bonded validators: rejected (O(N) every block; a hook
  fires only on the bonding event).
- Explicit MsgActivateValidator: rejected for v0.1 (extra manual step; genesis
  validators would still need separate handling).
