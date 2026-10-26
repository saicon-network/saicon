# ADR-0011: Emission, the single mint path, the 21M cap and the kill switch

Status: **Proposed** (2026-07-28). The *parameter block* (D1, D2, D5, D6) is recorded from the
**Decision Register v1.0** (adopted 2026-07-24, [DEVELOPMENT.md §2](../DEVELOPMENT.md), lines
67-75). The *mechanism* decisions — `x/mint` disposition, cap-overrun semantics, kill-switch
authority, fee routing (D3, D4, D7, D8) — are **recommendations awaiting owner sign-off**. Of
those four the Register defers exactly **one** to this ADR (`DEVELOPMENT.md:147` — "Disposition of
`x/mint` (zero/remove) decided in ADR-0011"); cap-overrun semantics, kill-switch authority and fee
routing have no Register text behind them at all — they are this ADR's own proposals, so what they
need is a first decision, not a ratification. The ADR **cannot be accepted** while the Q2
reward-source conflict (`DEVELOPMENT.md:154-155`) is open — see *Open questions*.

**Implementation status (2026-07-28, `main` @ `764dca8`).** Part of this ADR shipped ahead of its
acceptance, in `8e27782` ("feat(B8): single mint path with the 21M cap and an auditable kill
switch"): D3's belt-and-braces guard, D4's cap-as-precondition and kill-switch semantics, and D5's
carry are **built and tested** (`x/rewards/keeper/emission.go`, `x/rewards/keeper/cap_test.go`).
Not built: D1's clip budget, D2's gas offset, D6's VP-proportional split, D3's disposition of
`x/mint` (Option A applied to the *genesis template* only — the module is still in the binary),
and criterion 2's all-or-nothing epoch. D5's mechanism was **settled in favour of the shipped code** (offset, not addition) rather than the one
written here — recorded under *Consequences* — and the D7 param surface now being written into
the proto uses different field names, noted under D7.

Origin: Decision Register v1.0 §2.5 (`DEVELOPMENT.md:137-155`), calibration Stage 5
(`DEVELOPMENT.md:274`), integrated stress test long-horizon result (`DEVELOPMENT.md:291-293`).
Blockers: **B8** (single mint path + max supply), **B9** (post-max-supply security budget),
**B7** (commission economics), **B12** (Register↔code mapping) — `DEVELOPMENT.md:373-378`.
Related: [ADR-0007](ADR-0007-onchain-offchain-contract-cost-split.md) (money-touching logic is
on-chain native), [ADR-0008](ADR-0008-voting-power-and-selection.md) (VP formula — the reward
weight this ADR distributes by), [ADR-0010](ADR-0010-slashing-policy.md) (slashing),
ADR-0013 (bounded DAO authority, `DEVELOPMENT.md:195-199` — to write).

## Context

SAiCoN claims a fixed maximum supply of 21,000,000 SCN (`DEVELOPMENT.md:21-22`) and an emission
that pays for *measured participation*. Neither property is true of the running code. Verified
on `main` @ `65016c1` — the commit this ADR was written against. Rows 2, 5 and 7 have since been
changed by `8e27782`; the rest still hold at `764dca8` (see *Implementation status* above and
*Consequences*):

| Fact today | Evidence |
|---|---|
| `x/rewards` mints a **flat** `reward_per_epoch_amount` per eligible validator: `1000000` nscn = **0.01 SCN** | `x/rewards/types/params.go:6,22`; `x/rewards/keeper/emission.go:98` |
| the mint happens **inside the validator loop** — N mint operations, N supply mutations, no pool | `x/rewards/keeper/emission.go:169` |
| the reward is sent **whole to the operator account** — no commission split, no delegator share | `x/rewards/keeper/emission.go:173-174` |
| `x/mint` is wired into the app **and runs first in `BeginBlock`** | `app/app_config.go:124` (BeginBlockers[0]), `:233-234` (module), `:85` (Minter perm), `:160` (InitGenesis) |
| `x/mint` runs SDK defaults — no genesis override anywhere in the repo: inflation min 7% / max 20% / goal-bonded 67% | `cosmos-sdk@v0.53.6/x/mint/types/params.go:26-36`; no `mint` key patched in `docker/entrypoint-singlenode.sh` or `docker/testnet/gen-genesis.sh` |
| both paths mint the **same denom**: `init --default-denom nscn` rewrites `sdk.DefaultBondDenom` before `DefaultGenesis`, so `mint_denom = nscn` | `docker/entrypoint-singlenode.sh:35`; `cosmos-sdk@v0.53.6/x/genutil/client/cli/init.go:137-139` |
| **no** supply cap, **no** clip budget, **no** gas offset, **no** remainder carry exist in code | `grep -rn "21000000\|max_supply\|MaxSupply" chain/ --include=*.go --include=*.proto` → no hits |
| `x/validatorregistry` holds a `Minter` permission it never uses | `app/app_config.go:92`; no `MintCoins` call in `x/validatorregistry/` |

So the chain has **two live mint paths at the same denom** — the dual-emission gap already named
in `DEVELOPMENT.md:37` and `:60-63` — and nothing that stops either one at 21M. The Register set
the economics (§2.5); what is missing is the *mechanism*: exactly one place that creates SCN,
a cap that binds before it, and a switch that stops it. That is blocker **B8**, whose acceptance
criterion is written as a test obligation: "integration tests: no double mint, exact
conservation, hard 21M, kill switch" (`DEVELOPMENT.md:374`).

This ADR gates the Q3 emission rework (`DEVELOPMENT.md:327-328`). It must therefore be precise
enough to build from, and honest about which of its numbers were measured and which were
asserted (`DEVELOPMENT.md:297-316`).

## Decision

### D1 — Annual security budget (RECORDED — Register, adopted)

```text
AnnualSecurityBudget = clip( 0.15 · TotalBonded ,
                             0.005  · CurrentSupply ,     # floor
                             0.0075 · CurrentSupply )     # ceiling

EpochTarget = AnnualSecurityBudget / epochsPerYear
```

| Value | Register | Status |
|---|---|---|
| stake-linked term | 0.15 × TotalBonded | adopted, no recorded sweep (`DEVELOPMENT.md:139-140`) |
| floor / ceiling | 0.005 / 0.0075 × CurrentSupply | adopted, no recorded sweep |
| resulting payout | **238 SCN/day** medium · **357 SCN/day** max at the current snapshot | adopted (arithmetic on the rows above) |
| rejected alternative | **1,100 SCN/day** — 46-70% APR, 9-year pool runway | rejected (`DEVELOPMENT.md:141`) |

*Independent recomputation* (script, not eyeball; `CurrentSupply ≈ 17,375,600 SCN` per Stage 5,
`DEVELOPMENT.md:274`): floor 86,878 SCN/yr = **238.02 SCN/day**; ceiling 130,317 SCN/yr =
**357.03 SCN/day**; the stake term binds only for `TotalBonded ∈ [579,187 … 868,780]` SCN, so at
the global bonded cap (~870,000 SCN, `DEVELOPMENT.md:125`) the **ceiling** binds. The rejected
1,100/day = 401,500 SCN/yr → **69.3%** APR at the lower bonded bound, **46.1%** at the cap, and
consumes the remaining 3,624,400 SCN of headroom in **9.03 years**. Every headline number in the
Register reproduces exactly.

**Calibration basis.** Stage 5 of the seven-stage program: 4 bonded scenarios × commission × gas
× pool (`DEVELOPMENT.md:274`). §3.4 lists "emission rejection & APR math" and "gas-offset supply
horizon" among the **quantitatively grounded** results (`DEVELOPMENT.md:300-302`) — this is one of
the stronger parts of the program. What Stage 5 grounds is the *rejection* of 1,100/day and the
gas-offset horizon; the three clip constants (0.15 / 0.005 / 0.0075) were inputs to that sweep,
not outputs of it, and DEVELOPMENT.md records no derivation for them — they are adopted, not
measured. The whole-program caveat still applies: synthetic behaviour over real balances, DevNet
baseline until B2 recalibration (`DEVELOPMENT.md:313-316`).

`epochsPerYear` must stay **block-derived, never wall-clock** (§2.1, `DEVELOPMENT.md:91-93`).
Recommended: derive it, do not store it twice —
`epochsPerYear = blocks_per_year / x/participation.epoch_length_blocks` (integer division, guard
zero), with `blocks_per_year` a rewards param (x/mint precedent: 6,311,520 at 5s blocks). At the
devnet default of 100-block epochs that is 63,115 epochs/yr → ceiling budget ≈ 206.5 M nscn
(2.07 SCN) per epoch; at a production ~24h epoch, 357.03 SCN per epoch.

### D2 — Gas fees offset issuance (RECORDED — Register, adopted)

```text
Emission = max(0, EpochTarget − GasFees)
```

`TotalRewardPool` is **not** defined here — D2 is only about how fees displace issuance. The pool
has exactly one definition, in D4, and it includes the D5 carry.

**Intent, stated so it is not misread:** fees **substitute for** issuance, they do not add to it.
The security budget is a target *total* paid to validators; every SCN of fee revenue displaces one
SCN of newly-created supply. At roughly 100k SCN/year of fees, minting stops entirely and the
21M cap is never reached (`DEVELOPMENT.md:142-143`, `:274`, `:291-293`).

*Honest nuance the Register's one-liner hides:* 100k SCN/yr zeroes issuance only against the
**floor** band (target 86,878 SCN/yr). Against the **ceiling** band (130,317 SCN/yr) the same fee
revenue still leaves **30,317 SCN/yr** of minting. "Minting stops at ~100k" is true for the
medium case, not universally.

**Open sub-decision — where `GasFees` comes from (this ADR's own finding, not in the Register).**
`x/distribution` runs at `BeginBlockers[1]` (`app/app_config.go:125`) and sweeps the entire fee
collector into stake-weighted validator rewards every block. If `x/rewards` also counts gas fees
into its pool, the same fees are paid **twice** — once stake-weighted and ungated by
`x/distribution`, once VP-weighted and EP-gated by `x/rewards`. That is precisely what B8's
"exact pool conservation" test must catch. Options:

- **F1 — sweep.** `x/rewards.BeginBlock` moves the fee-collector balance into the rewards pool
  before `x/distribution` sees it (requires reordering `rewards` ahead of `distribution` in
  `app/app_config.go:123-134`). `x/distribution`'s allocation goes dark; commission and delegator
  rewards then have **no** implementation (see D6).
- **F2 — issuance only.** Leave fees with `x/distribution`; drop the `GasFees` term from D4's pool
  definition (`TotalRewardPool = Emission + carry_over_old`). Simplest, but contradicts
  `DEVELOPMENT.md:143` as written and leaves an ungated stake-weighted reward stream that bypasses
  the PoP gate.
- **F3 — route and re-use (RECOMMENDED).** `x/rewards` owns the epoch pool (fees + issuance) and
  pays each validator's share **through** `x/distribution.AllocateTokensToValidator`
  (`cosmos-sdk@v0.53.6/x/distribution/keeper/allocation.go:85`), which splits commission vs
  delegator shares and credits the existing withdrawal accumulators. One pool, one gate, and the
  stock commission/withdrawal machinery is reused instead of reinvented.

F3 is a recommendation, not a Register decision. It requires disabling `x/distribution`'s own
per-block allocation (or emptying the fee collector before it runs) — the change must be proven
by the conservation test in *Acceptance criteria*, not assumed.

### D3 — The single authoritative mint path (B8) — disposition of `x/mint` (RECOMMENDED)

**Rule.** The epoch pool is minted **exactly once**, by `x/rewards`, at the epoch boundary,
before distribution. No other module may create SCN. `x/mint` and `x/rewards` must never mint
simultaneously — **today they both can** (see *Context*).

Two dispositions, both mechanically viable:

**Option A — zero `x/mint`, keep the module.**
Mechanism: set `mint.params.inflation_min = inflation_max = inflation_rate_change = 0` and
`mint.minter.inflation = annual_provisions = 0` (keep `goal_bonded` at 0.67 — zero is rejected by
`validateGoalBonded`, `cosmos-sdk@v0.53.6/x/mint/types/params.go:125-127`). With
`min = max = 0`, `NextInflationRate` clamps to 0 every block
(`.../x/mint/types/minter.go:61-66`), `BlockProvision` is 0, `sdk.NewCoins` drops the zero coin
and `Keeper.MintCoins` returns early on the empty set (`.../x/mint/keeper/keeper.go:116-120`).
On a running chain: one governed `MsgUpdateParams` to x/mint. **No migration, no store surgery.**
Cost: the mint BeginBlocker and the module account's `Minter` permission stay in place, so "no
double mint" is a *parameter* property that a single governance vote can undo. §2.10
(`DEVELOPMENT.md:195-199`) does not currently forbid that vote.

**Option B — remove `x/mint` from the app (RECOMMENDED).**
Mechanism: delete the module entry (`app/app_config.go:232-235`), the BeginBlocker entry
(`:124`), the InitGenesis entry (`:160`), the `Minter` module-account permission (`:85`), the
blocked-account entry (`:99`), the imports (`:20`, `:67-68`), and the keeper field/injection
(`app/app.go:40`, `:91`, `:184` — nothing else in the app uses `MintKeeper`).
Migration consequence: for a **fresh genesis** this is free, and a DevNet genesis restart under
Register parameters is explicitly authorized (`DEVELOPMENT.md:74-75`, roadmap item (4),
`DEVELOPMENT.md:330`). For an **already-running** chain it is state-breaking: an upgrade handler
must delete the `mint` store (`storetypes.StoreUpgrades{Deleted: ["mint"]}`), the mint module
account must be retired, and every client reading `/cosmos/mint/v1beta1/inflation` breaks —
including the ping.pub explorer fork, whose feature whitelist must drop inflation.

**Recommendation: B, executed at the Q3 DevNet genesis restart**, with A as the documented
fallback for any chain that cannot restart. Reason: B8 is a money invariant, and structural
enforcement ("the code that mints is not in the binary") is strictly stronger than parameter
enforcement ("the code that mints is configured to mint zero"). The window in which B is free is
open right now and closes at MainNet genesis.

**Regardless of A or B**, the belt-and-braces guard is required in `x/rewards`, because it is the
only guard that survives a future param change: the epoch pool is computed once, minted once, and
asserted against the cap before the mint (D4). This closes the `x/mint` half of **B12**
(`DEVELOPMENT.md:378`: "`x/mint` disposition executed").

### D4 — 21M max supply enforced + kill switch (RECOMMENDED)

**Where the check belongs.** In the **rewards keeper, before the mint**, not in bank, not in an
invariant that fires afterwards. An `EndBlock` invariant that panics *after* a cap breach halts
the chain instead of preventing the breach; the cap must be a precondition of `MintCoins`.

```text
capNscn         = 21_000_000 · 10^8 = 2_100_000_000_000_000       # fits uint64 and sdkmath.Int
currentSupply   = bank.GetSupply(ctx, "nscn").Amount
headroom        = max(0, capNscn − currentSupply)
Emission        = min( max(0, EpochTarget − GasFees), headroom )  # cap binds on ISSUANCE only
TotalRewardPool = Emission + GasFees + carry_over_old             # THE definition (D5)
#   where Emission already has carry_over_old SUBTRACTED from the target (D5):
#   an epoch pays EpochTarget, never more — the carry offsets issuance
```

`TotalRewardPool` is defined here and nowhere else; every later use — D5, D8, acceptance
criterion 2 — means this expression, carry included.

**The cap binds on issuance, not on the pool.** Gas fees are already in circulating supply;
re-routing them does not create SCN. Applying the cap to `TotalRewardPool` would wrongly stop
paying validators out of fee revenue after the cap. This distinction is load-bearing for the
conservation test.

**Overrun semantics: mint the remainder up to the cap, then stop** — recommended over refusing
entirely. Refusing produces a discontinuous cliff (full pay in epoch *n*, zero in *n+1*, with
headroom stranded forever); minting the remainder is monotone, exactly testable
(`supply == cap` afterwards), and leaves the chain still paying validators from `GasFees`. Note
that "then stop" *is* the terminal state whose economics are undefined — **B9**, see *Open
questions*.

**Kill switch.** Keep the existing `x/rewards` param `enabled` (`proto/chain/rewards/v1/params.proto:26`;
`x/rewards/types/params.go`, `DefaultParams` — default `true`) as *the* switch, and make it
authoritative once `x/mint` is disposed of. Semantics to implement:

- `enabled = false` ⇒ no mint, no fee sweep into the pool, **no remainder accrual**, and a
  `RewardSnapshot` per validator recording the reason (`"emission disabled"`), so the halt is
  auditable rather than silent.
- **Where the fees go while it is off.** Under F3 the fee collector is emptied by `x/rewards`, so
  "no fee sweep" would otherwise leave the fees with no owner. Disposition: **they stay in the fee
  collector and are swept into the first pool after re-enable** — `x/distribution`'s own
  allocation stays disabled throughout. The alternative (hand the fees back to `x/distribution`
  for the duration) is rejected: it would pay validators stake-weighted and ungated exactly while
  the participation layer is switched off, which is the "bypass/disable the participation layer"
  §2.10 forbids. Accruing them instead makes the switch a pure *stop*: nothing is created, nothing
  is redirected, and the first epoch after re-enable pays the accumulated fees through the gate.
- **Who may flip it:** the `x/gov` module account only — the module authority set at wiring
  (`x/rewards/keeper/keeper.go:22-24`, enforced in `x/rewards/keeper/msg_update_params.go:18-21`).
  No emergency multisig, no operator override.
- **§2.10 bound (`DEVELOPMENT.md:195-199`).** The DAO may **stop** emission; it may **never**
  redirect it. Off means nothing is minted and nothing accrues to anyone — in particular not to a
  treasury, a foundation account or the validator owner. There is a real tension here that
  ADR-0013 must resolve explicitly: §2.10 forbids the DAO from "bypass/disable the participation
  layer", and an indefinitely-disabled reward switch is one way to do exactly that. §2.10 is
  **silent** on how long the switch may stay off, so the bound below is this ADR's own proposal,
  not a Register decision — the same standing as the D7 budget-rate bounds, and ratified or
  replaced by the same ADR-0013. Proposed bound: `enabled=false` is an incident control, and any
  period longer than N epochs requires a re-ratifying proposal. N is not decided here.

### D5 — Rounding remainder carried forward (RECORDED — Register: "APPROVE NOW")

The per-validator split of an integer pool leaves a remainder in base units. It is **carried
forward in the same reward pool to the next epoch**, in `nscn`. It is **never** paid to the
validator owner, a proposer or a treasury, and never *credited* to the module as revenue — it is
held in the rewards module account solely as next epoch's pool input, and it is the **only** thing
that account holds between epochs (`DEVELOPMENT.md:150-151`, `:217`).

**The carry OFFSETS issuance; it does not enlarge the pool.** (Project decision, 2026-07-28,
resolving the divergence this ADR previously recorded as open.)

```text
carry_over_old  = the remainder held in the module account from earlier epochs
Emission        = clip( max(0, EpochTarget − GasFees − carry_over_old), 0, headroom )
TotalRewardPool = Emission + GasFees + carry_over_old      # == EpochTarget when unclamped
```

The alternative — adding the carry *on top of* the epoch target — also conserves and also keeps
dust away from validators, and it is the more literal reading of the Register's "carried forward
in the same reward pool". It was rejected because it lets an epoch pay **more than its own
budget**: the security budget is a ceiling on what an epoch is worth, and a rounding artefact
must not raise it. Offsetting keeps every epoch's payout at exactly `EpochTarget`, makes
cumulative issuance monotonically lower by the standing carry, and is the same shape as the
gas-fee offset in D2 — value already in hand substitutes for minting rather than adding to it.
The standing difference between the two rules is bounded by `N−1` base units, where `N` is the
number of eligible validators.

This is an edit to a Register-recorded decision (`DEVELOPMENT.md:150-151`), taken deliberately
and recorded here rather than left as silent drift.

Implementation: a persisted `carry_over` item in the rewards store (`Keeper.Remainder`), read
before the mint is sized and rewritten with the new remainder after the split. It is inside the
conservation identity of the acceptance test — not a side account. On `enabled=false` it does not
grow (D4). The mint happens **before** the payability check, so the carry only ever describes
base units that actually exist: carrying a remainder that was never minted would break
conservation silently.

### D6 — Distribution ∝ final VP, EP gate underneath, commission (RECORDED)

- `reward(v) = TotalRewardPool · VP(v) / Σ VP` over the **eligible active set**
  (`DEVELOPMENT.md:148`, `:258`). `VP = min(bonded, validatorCap) · WARP/100 · C` — defined by
  [ADR-0008](ADR-0008-voting-power-and-selection.md), which this ADR depends on and does not
  pre-empt. **No tiers, no second KPI multiplier** (`DEVELOPMENT.md:113-114`).
- The **EP gate stays underneath**: only `ACTIVE ∧ C=1 ∧ EP>0` receives, invariant V-2
  (`DEVELOPMENT.md:51`, `:259`). This gate already exists and works
  (`Keeper.classify` in `x/rewards/keeper/emission.go`); the rework replaces the *amount*, not
  the gate.
- Commission: **10% default, governance range 5-15%** — flagged 🔒 **"not economically proven"**,
  blocker **B7** (`DEVELOPMENT.md:149`, `:373`). Recorded as the Register's value, **not**
  endorsed as validated: B7's acceptance criterion is that SCN price, liquidity and validator
  OPEX make the budget and the commission support real operations, and that work has not been
  done.

### D7 — Parameter surface to add to `x/rewards`

| Param | Type | Default | Authority | Note |
|---|---|---|---|---|
| `enabled` | bool | `true` | gov | exists today; semantics extended per D4 |
| `bonded_budget_rate` | LegacyDec | `0.15` | gov, `∈ [0.10, 0.20]` | stake-linked term |
| `supply_budget_floor` | LegacyDec | `0.005` | gov, `∈ [0.0025, 0.0075]` | clip floor |
| `supply_budget_ceiling` | LegacyDec | `0.0075` | gov, `∈ [floor, 0.01]` | clip ceiling; must be ≥ floor |
| `blocks_per_year` | uint64 | `6311520` | gov | epoch divisor is block-derived (§2.1) |
| `max_supply` | uint64 (nscn) | `2100000000000000` | **not gov-tunable** | see below |
| `carry_over` | state item, not a param | `0` | — | D5 |

**The three numeric bounds above are this ADR's own proposal, not a Register decision.** §2.10
lists cap percentages and the commission range among the DAO's tunables and is **silent** on the
emission-budget rates (`DEVELOPMENT.md:197-199`), so there is no Register basis to cite — the same
situation as the kill-switch duration bound in D4, and handled the same way: the ADR proposes,
ADR-0013 ratifies or replaces. Until then the bounds are enforced in `Params.Validate()`, which
must reject an out-of-range rate rather than clamping it silently. Rationale for the widths:
`0.10-0.20` keeps the stake term within ±⅓ of the adopted 0.15 without letting one vote double
the security budget; `≤ 0.01` holds the ceiling at or below 1% of supply per year, which is what
keeps B9's quarter-century runway a schedule rather than a variable; `ceiling ≥ floor` because an
inverted clip emits the floor forever.

`reward_per_epoch_amount` (`params.proto:22`) is **removed** — it is the mock. `max_supply`
should be a compile-time constant or a param the gov authority cannot raise; §2.10 does not list
supply among the DAO's tunables (`DEVELOPMENT.md:197-199`), and a raisable cap is not a cap.

Two new expected-keeper methods are required (`x/rewards/types/expected_keepers.go`):
`BankKeeper.GetSupply` (cap check) and `StakingKeeper.TotalBondedTokens` (budget), plus whatever
F3 needs from `x/distribution`. `GetSupply` landed with `8e27782`; at `764dca8` nothing else in
this table exists — see *Consequences*.

*Naming note.* The param surface being written into `proto/chain/rewards/v1/params.proto` while
this revision was made uses `budget_bonded_rate` / `budget_floor_rate` / `budget_ceiling_rate`,
not the `bonded_budget_rate` / `supply_budget_floor` / `supply_budget_ceiling` above, and its
`Validate()` bounds the rates only to `[0, 1]` with `ceiling ≥ floor` — not to the narrower
governance bounds this table specifies. Whichever names win, one of the two has to change, and the
bounds still need enforcing.

### D8 — Execution order

1. `x/participation` EndBlock computes PS/EP for the closing epoch (unchanged; already ordered
   before rewards, `app/app_config.go:141` and the `MaybeRunEpoch` doc comment in
   `x/rewards/keeper/emission.go`).
2. `x/rewards` EndBlock, on an exact epoch boundary only:
   read params → if `!enabled` stop (snapshots only) → compute `TotalBonded`, `CurrentSupply`,
   `GasFees` → `EpochTarget` → `Emission` (gas offset, then cap clamp) → **one** `MintCoins` of
   `Emission` (target minus fees minus carry) → pool = `Emission + GasFees + carry_over` →
   split ∝ VP over the gated set →
   pay → persist the new `carry_over` → snapshots → V-2 assertion.

## Acceptance criteria (these become the integration tests — B8)

Stated as properties, each of which failed on the code as it stood at `65016c1`. `8e27782` has
since made some of them pass at unit level — which of them, and which clauses are still unwritten,
is in *Consequences*:

1. **No double mint.** Over any block range, the total increase in `bank` supply of `nscn` equals
   the sum of `Emission` values recorded by `x/rewards` for the epochs in that range. Under
   disposition B, additionally — and stated as something a test can actually read, since
   `authtypes.Minter` is a module-account permission and not denom-scoped: the set of module
   accounts holding `Minter` in `maccPerms` is exactly `{rewards, ibc-transfer}`
   (`app/app_config.go:85,92-93`), `mint` is absent from the module manager and from
   `BeginBlockers`, and `ibc-transfer` can only mint IBC voucher denoms, never `nscn`.
2. **Exact pool conservation.** For every epoch:
   `Σ reward(v) + carry_over_new == Emission + GasFees + carry_over_old`, exactly, in `nscn`
   (no `LegacyDec` rounding leak), and after the epoch the rewards module-account balance equals
   exactly `carry_over_new` and nothing else. A partially-emitted epoch is a failure — the epoch's
   payouts either all commit or none do.
3. **Hard 21M cap.** Property test over randomized supply/bonded/fee inputs including
   `currentSupply` one unit below the cap: after any number of epochs,
   `supply(nscn) ≤ 2_100_000_000_000_000`, and once the cap is reached `Emission == 0` while
   fee-funded payouts continue.
4. **Kill switch works.** With `enabled=false`: supply is unchanged across an epoch boundary, no
   payout is made, `carry_over` is unchanged, and every validator has a `RewardSnapshot` with the
   disabled reason. Flipping it back on resumes with the carry intact. And, per D4's fee
   disposition: with `enabled=false` for N epochs then re-enabled, **no fee revenue is lost or
   double-counted** — the fees collected over those N epochs appear exactly once, in the first
   pool after re-enable.
5. **Gate preserved.** V-2 (`EP=0 ⇒ reward=0`) still holds under the new amount formula
   (`x/rewards/keeper/invariants.go:28`), and a validator excluded by the gate contributes **no**
   VP to the denominator.
6. **Budget clip.** Table test over the three regimes (stake term below floor / inside band /
   above ceiling) reproducing 238.02 and 357.03 SCN/day at the Stage-5 snapshot.

## Open questions — this ADR does **not** close them

- **Q2, the reward source (🔒 owner sign-off required).** The management questionnaire answered
  **"pre-funded pool"**; the Register and every calibration study assume **issuance + fees**
  (`DEVELOPMENT.md:154-155`, gate listed at `:381`). These are different chains: a pre-funded
  pool needs a genesis allocation, a vesting/escrow account and no minting at all — in which case
  D1-D4 collapse to "pay out of an account", the 21M cap is trivially satisfied, and `x/mint`
  disposition is still required but for a different reason. Everything in this ADR is written for
  **issuance + fees**. The conflict must be closed explicitly by the owner; until it is, this ADR
  stays *proposed* and the implementation should not start.
- **B9, terminal economics (🔒).** At zero gas the cap is reached and gross APR goes to zero.
  Independent recomputation, **assuming the ceiling binds throughout** — which requires
  `TotalBonded` to grow with supply to ≥ `0.05 · CurrentSupply`, i.e. past **1,050,000 SCN**, well
  beyond today's ~870,000 global bonded cap (`DEVELOPMENT.md:125`): compounding the 0.75% ceiling
  from 17,375,600 SCN reaches 21,000,000 in **25.4 years**, which is where the Register's
  "≈ year 26" (`DEVELOPMENT.md:152-153`, `:291-293`) comes from; at the 0.5% floor it is 38 years.
  That assumption is exactly the one D1 refutes at today's snapshot. With `TotalBonded` **pinned**
  at the current global cap, the stake term flattens at `0.15 · 870,000` = **130,500 SCN/yr** the
  moment supply passes 17.4M, and the cap is reached in **≈27.8 years** instead — two years later,
  by a different mechanism, so the Register's ballpark survives but not its reasoning. What funds
  security after that — fee targets, reserves, or a terminal issuance tail — is **undefined**.
  D4's "mint the remainder, then stop" is a *mechanism* for the terminal state, not an *answer*
  to it.
- **B7, commission (🔒).** 10% / 5-15% recorded, "not economically proven".
- **Fee routing (D2, F1/F2/F3)** — a recommendation, owner/architect ratification needed.
- **Kill-switch duration bound (D4)** — the "N epochs before re-ratification" number is not set.
- **Budget-rate governance bounds (D7)** — `[0.10,0.20]` / `[0.0025,0.0075]` / `[floor,0.01]` are
  this ADR's proposal on a §2.10 silence, same standing as the bound above; ADR-0013 owns both.

## Consequences

- **What `8e27782` already closed** (built ahead of this ADR's acceptance — so it is the code that
  must be read against the ADR, not the ADR against the code).
  - The pool is minted **once** per epoch, by the single `MintCoins` in
    `Keeper.DistributeEpochRewards` (`x/rewards/keeper/emission.go`) and no longer inside the
    payout loop, so the total emitted is no longer an emergent property of a loop. Criterion 1's
    first clause is now testable and tested (`x/rewards/keeper/cap_test.go`,
    `TestEmission_MintsExactlyOncePerEpoch`).
  - The 21M cap exists and binds **before** the mint (`types.MaxSupplyNscn`,
    `Keeper.mintHeadroom`) — exactly where D4 puts it. At the cap emission stops without erroring
    and records `"max supply reached"` per validator.
  - The carry exists as a persisted store item (`Keeper.Remainder`, `GetRemainder`/`SetRemainder`)
    and never reaches a validator owner — D5's *rule*, though not D5's *mechanism* (next bullet).
  - The kill switch does what D4 specifies: no mint, no accrual, and a `RewardSnapshot` carrying
    the reason for every validator, so a disabled epoch is documented rather than silent.
  - Dual emission is closed **for new chains only**: `docker/entrypoint-singlenode.sh:62-76` now
    zeroes `mint.minter.inflation` and the three inflation params in the genesis template — this
    ADR's Option A, applied to the template rather than by governance. `x/mint` is still in the
    binary and still `BeginBlockers[0]` (`app/app_config.go:124`, `:233-234`), so D3's
    recommendation (Option B) is **not** executed and B12's "`x/mint` disposition executed"
    (`DEVELOPMENT.md:378`) stays open. On any chain not started from the patched template, the
    dual mint path is still live.
- **Where the implementation DIVERGED from this ADR: the carry is an offset, not an addend.**
  D5 and D8 say the carry is *added to the pool* before the split
  (`pool = Emission + GasFees + carry_over_old`). The code does the opposite — it *subtracts* the
  carry from what it mints (`toMint := target.Sub(carried)` in `DistributeEpochRewards`), so the
  pool stays at the epoch target and the carry substitutes for issuance the way gas fees do. The
  code says so in as many words: "a remainder … REDUCES the next epoch's fresh issuance rather
  than adding to the pool". Both conserve,
  both keep dust away from validators, and the standing difference is at most `N−1` base units.
  They are still not the same rule: under this ADR an epoch may pay out *more* than its budget by
  the carried remainder, under the code it never can, and cumulative issuance under the code is
  lower by the standing carry. The code's version is arguably the better rule — it makes
  `EpochTarget` a true per-epoch ceiling — but it edits a Register-recorded decision ("carried
  forward in the same reward pool to the next epoch", `DEVELOPMENT.md:150`), so either D5/D8 or
  the code has to move, and that choice is the owner's, not the implementer's.
- **Implementation debt still open after `8e27782` (code still contradicts the Register).**
  - The reward amount is still a flat **0.01 SCN** per eligible validator per epoch
    (`x/rewards/types/params.go`, `DefaultRewardPerEpochAmount`); the pool is
    `reward_per_epoch_amount × |eligible|` — a *pooled mock*, not D1's clip budget. At the devnet
    100-block epoch that is ~1.73 SCN/validator/day; the Register's whole-network budget is
    238-357 SCN/day. D7 says this param is removed; it is still the only input to the pool.
  - There is still **no** clip budget, **no** gas offset and **no** VP-proportional split —
    D1, D2 and D6 are entirely unbuilt, and nothing reads `TotalBonded` or the fee collector.
  - The kill switch is still **not** what §1.1 implies. `enabled` stops `x/rewards` only; a chain
    whose genesis was not patched keeps inflating through `x/mint` with `enabled=false`, and even
    on a patched chain the zeroing is a *parameter* one gov vote can undo. Calling it "the
    kill-switch" (`DEVELOPMENT.md:36`) still overstates it until D3-B is executed.
  - Emission is still **not atomic**: a send/store error mid-loop returns from
    `DistributeEpochRewards` and `MaybeRunEpoch` deliberately swallows it to avoid halting
    (`x/rewards/keeper/emission.go`), so a **partially emitted epoch commits to state**.
    Acceptance criterion 2 forbids this; the rework must make the epoch payout all-or-nothing
    (cached context or precomputed-then-applied). `8e27782` moved the mint out of the loop but
    left the payout loop non-transactional.
  - Commission has **no substrate**: rewards go whole to the operator account
    (`SendCoinsFromModuleToAccount` to `sdk.AccAddress(valAddr.Bytes())`), so delegators currently
    receive nothing from the PoP path. Further, stock `x/staking` has `MinCommissionRate` but
    **no maximum** (`cosmos-sdk@v0.53.6/x/staking/types/params.go:34-35`), so the Register's
    5-15% *range* is only half-enforceable without custom validation.
  - `x/validatorregistry` holds an unused `Minter` permission (`app/app_config.go:92`) — dormant
    mint authority that should be dropped as part of "one authoritative minter", and which
    criterion 1's revised second clause now names explicitly.
  - Minor, spotted in passing: `proto/chain/rewards/v1/params.proto:13,20-21` documents the reward
    denom as `"scn"`; the code mints `"nscn"` (`x/rewards/types/params.go`, `RewardDenom`). Stale
    comment, worth fixing in the same change so the 8-decimal redenomination is not misread.
- **Removing `x/mint` (D3-B) breaks inflation-shaped clients.** The explorer fork and any wallet
  or dashboard reading `/cosmos/mint/v1beta1/*` must be updated in the same change; the roadmap
  site's economics page must state "fixed supply, no inflation module" rather than an inflation
  rate.
- **Validator income becomes bounded and visibly smaller.** 238-357 SCN/day across the whole
  active set is roughly a third of the rejected 1,100/day. Communications and the validator
  handbook must carry the honest APR, not the mock.
- **A fee-funded chain eventually stops minting** — that is the design intent (D2), and it makes
  B9 a *scheduled* problem with a known date rather than a hypothetical one.
- **The tests are the deliverable.** B8 is closed by the six properties above passing, not by the
  code merging. `8e27782` covers criteria 1 (first clause), 3 and 4 at unit level and criterion 2
  only for the mock pool — criterion 2's all-or-nothing clause, criterion 4's fee clause, criterion
  5 under the new formula and criterion 6 are all still unwritten, and none of it is an
  *integration* test yet, which is what `DEVELOPMENT.md:374` asks for. Until they pass,
  `DEVELOPMENT.md:353` stands: no real payouts.

## Alternatives considered

- **Keep the flat per-validator reward for DevNet, defer everything.** Rejected: the flat mock
  cannot exercise a cap, a pool, a remainder or a gas offset, so it cannot produce the evidence
  B8 asks for, and every extra month it runs deepens the gap between the site's economics and the
  chain's.
- **Enforce the cap in `x/bank` (a wrapped `MintCoins` that refuses above 21M).** Attractive
  because it binds *every* minter, including modules added later. Rejected as the primary
  mechanism: it forks a core SDK keeper, hides a money rule in an unexpected place, and turns a
  cap breach into a returned error at an arbitrary call site. Worth revisiting as a *second* line
  once the rewards-side check exists.
- **Enforce the cap as an `EndBlock` invariant only.** Rejected: it detects a breach after supply
  has already been created and its fail-loud response is a chain halt (the V-1/V-2 pattern,
  `DEVELOPMENT.md:46-57`). Appropriate as an additional assertion, not as the enforcement point.
- **Pay the rounding remainder to the block proposer or to a treasury.** Rejected by the Register
  (`DEVELOPMENT.md:150-151`): it creates a proposer-timing incentive at epoch boundaries and a
  silent revenue stream outside the participation gate. Carry-forward keeps every base unit inside
  the audited pool.
- **`x/mint` with a custom `MintFn` implementing the clip budget** (SDK v0.53 supports
  `WithMintFn`, `.../x/mint/keeper/keeper.go:39-49`). Genuinely viable: one minter, stock module,
  no removal. Rejected because the budget depends on the reward gate, VP and the epoch boundary,
  all of which live in `x/rewards`; putting the money logic in a BeginBlocker of a different
  module splits the pool computation from the pool distribution and makes conservation harder to
  test. Reconsider if `x/mint` must be retained for client compatibility.

## References

- [DEVELOPMENT.md](../DEVELOPMENT.md) — §2.5 emission (`:137-155`), §2.1 epoch (`:89-93`), §2.10
  governance bounds (`:195-199`), §3.1 formulas (`:254-259`), §3.2 Stage 5 (`:274`), §3.3
  long-horizon supply (`:291-293`), §3.4 measured-vs-asserted (`:295-316`), §5 blockers B7/B8/B9/B12
  (`:373-378`), approval gates (`:380-383`), Q3 roadmap item 2 (`:327-328`).
- [ADR-0007](ADR-0007-onchain-offchain-contract-cost-split.md) — money-determining logic is
  on-chain, native.
- [ADR-0008](ADR-0008-voting-power-and-selection.md) (VP + selection) — defines the `VP` this ADR
  distributes by; [ADR-0010](ADR-0010-slashing-policy.md) (slashing); ADR-0013 (bounded DAO
  authority — the kill-switch bound and the D7 budget-rate bounds; to write).
- Cosmos SDK v0.53.6: `x/mint/keeper/mint.go:19-63`, `x/mint/types/minter.go:46-69`,
  `x/mint/types/params.go:26-36,107-133`, `x/distribution/keeper/allocation.go:85`,
  `x/staking/types/params.go:34-35`, `x/genutil/client/cli/init.go:137-139`.
- Code: the *Context* table is as of `main` @ `65016c1`; the *Implementation status* and
  *Consequences* notes are as of `main` @ `764dca8` (after `8e27782`) —
  `x/rewards/keeper/emission.go`, `x/rewards/keeper/cap_test.go`, `x/rewards/types/params.go`,
  `x/rewards/types/expected_keepers.go`, `app/app_config.go`, `app/app.go`,
  `docker/entrypoint-singlenode.sh`.
