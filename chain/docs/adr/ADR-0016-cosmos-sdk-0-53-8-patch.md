# ADR-0016: Cosmos SDK v0.53.8 — the security patch rides the v0.10.0 upgrade

Status: **Accepted** (2026-10-28, project decision) — implemented in S8 and released in v0.10.0
(Cosmos SDK v0.53.8, CometBFT v0.38.25, ibc-go v10.7.0 in `chain/go.mod`), applied by the `v0.10.0`
in-place upgrade. Amends the SDK and CometBFT rows of
[ADR-0002](ADR-0002-cosmos-sdk-tooling-version-pin.md), whose Ignite and Go pins are unchanged.
Related: [ADR-0015](ADR-0015-sentry-layout-and-node-exposure.md) (the public sentry layout the
CometBFT hardening protects), the `v0.10.0` runbook
the devnet operations runbook.

## Context

The chain is built on Cosmos SDK v0.53.6, CometBFT v0.38.21 and ibc-go v10.4.0: release family
2025.1 of the Cosmos stack, which is still supported. Since that pin, upstream has shipped three
sets of fixes inside the same family.

**Cosmos SDK v0.53.8** (27 July 2026). Its release notes call it "important security fixes",
advise every chain to take it through a coordinated upgrade, and flag it state-breaking. No
advisory (GHSA, ISA or CVE) has been published for it, so severity and exploit conditions are
not stated upstream; we treat it as high until shown otherwise. The changes that reach this
chain:

- `x/auth/tx` and the ante handler: a transaction whose SignerInfos and signers disagree in
  count, or that carries a nil public key or a nil multisig mode, now fails to decode instead
  of panicking; `SetPubKeyDecorator` rejects extra SignerInfos; multisig bit arrays and nested
  signatures are bounded (depth 2, breadth 32). Our ante chain wraps the SDK's, so it inherits
  all of this.
- `crypto`: the SEC1 tag byte of a secp256k1 public key is validated.
- `x/distribution`: reading a validator's historical rewards for a period that has no record
  is now an error, where it used to decode as zero and silently mis-pay; a reward or commission
  withdrawal that a staking change triggers without the staking messages' strict flag (in
  BeginBlock, EndBlock, or a module's direct keeper call) and that targets a blocked address
  now falls back to the owner and then to the community pool instead of
  failing, while the SDK's own `MsgDelegate`, `MsgUndelegate`, `MsgBeginRedelegate` and
  `MsgCancelUnbondingDelegation` keep the strict rule.
- `x/staking`: a redelegation that consumes the last shares of an unbonded validator, which
  `Unbond` then removes, completes instead of failing.
- `x/gov`: a proposal that cannot be decoded is failed and removed from its queue, and the
  EndBlocker carries on with the other proposals due in that block, where it used to stop at
  the first one (and, for a proposal still in its deposit period, left the queue entry behind).

The changelog also lists a cache-multistore trace-context race fix. Its code is in
`cosmossdk.io/store`, a separate module that v0.53.8 still pins at v1.1.2, the version this chain
already used, so the bump does not change the store code in the binary.

None of the `cosmossdk.io/*` modules moves: v0.53.8's own `go.mod` pins exactly the versions
this chain already uses.

**CometBFT v0.38.22–v0.38.25.** Evidence and blocksync hardening (light-client-attack evidence
validation, full commit verification in blocksync, sender and signature-count checks on
blocksync responses), nil-vote handling, a mempool heap-amplification filter, the
`double_sign_check_height = 1` off-by-one, and, from v0.38.22, a local check that rejects a
proposed block whose time is at least `block_time_tolerance` (default 60 s) ahead of the
node's wall clock. v0.38.26 adds only ML-DSA-65 signature verification, which this chain
does not use.

**ibc-go v10.5–v10.7.** v10.5.0 adds integrity validation of ProtoJSON interchain-account
packets and is state-breaking; v10.5.1 fixes a failure on non-UTF-8 values in error and event
emission; v10.6.0 fixes callbacks middleware, which this chain does not wire; v10.7.0 tightens
IBC v2 transfer validation. The ICA host lets a remote chain execute messages here, so the
v10.5.0 validation is the one that matters.

The SDK patch is state-breaking: a node on v0.53.8 cannot replay blocks produced under
v0.53.6 behaviour and agree on the app hash wherever the changed paths ran. It needs a
coordinated binary swap at a height. The `v0.10.0` upgrade already is one.

## Decision

**Take the patch inside release family 2025.1 and ship it in the `v0.10.0` binary:**

| Module | From | To |
|---|---|---|
| `github.com/cosmos/cosmos-sdk` | v0.53.6 | **v0.53.8** |
| `github.com/cometbft/cometbft` | v0.38.21 | **v0.38.25** |
| `github.com/cosmos/ibc-go/v10` | v10.4.0 | **v10.7.0** |

CometBFT goes past v0.38.23, the version SDK v0.53.8 was released against, because
v0.38.25 carries the mempool and blocksync hardening and makes no state-machine change. It adds
one local validity rule, `block_time_tolerance` (see Consequences), which is a node's own
decision about a proposed block, not a change to how state is computed. It stops short of
v0.38.26, whose only change is a signature scheme we do not use.

Nothing else is bumped on purpose. Minimal version selection moves three indirect modules
with CometBFT v0.38.25: `github.com/lib/pq` v1.10.9 → v1.12.0, `github.com/minio/highwayhash`
v1.0.3 → v1.0.4 and `github.com/sasha-s/go-deadlock` v0.3.5 → v0.3.9. The `replace` block is
unchanged. `indexer/` pins none of these modules and does not import the chain module, so its
`go.mod` does not move.

**No second halt.** The patch rides the `v0.10.0` upgrade height. The store loader and the
module migrations are unchanged by it: v0.53.8 adds no store, no migration and no module
consensus version, so `RunMigrations` is a no-op for the SDK's modules. (The handler itself gains
one unrelated step, the slashing fractions of ADR-0010's implementation note.)

## Consequences

**The swap is the one v0.10.0 already requires.** Every node changes binary at the upgrade
height, which is what the runbook does. The patch adds no operator step.

**History is replayed by the binary that made it.** From the upgrade height the v0.10.0
binary is authoritative; replaying genesis to that height needs the v0.9.1 binary, as it did
before this decision (cosmovisor keeps both).

**The distribution change turns an inconsistency into a halt.** A missing historical-rewards
record used to pay zero; it now fails the operation, and in a BeginBlock or EndBlock path that
stops the chain. This chain writes distribution state only through the SDK keepers and the
standard zero-height export, which is why we expect no record to be missing; what has been
shown is narrower. The rehearsal below ran a state a v0.9.1 binary built through the upgrade
height and the blocks after it on the new binary, and a probe found no record missing there.
The live DevNet's own state has not been read. The runbook carries a preflight for it: a REST
smoke test before the proposal (the running v0.9.1 reads a missing record as zero, so that test
cannot prove absence) and, conclusive, a read-only export of the halted state with a check that
every record the new code asks for exists
(the devnet operations runbook, 0.2 and 3.3).

**A new event for indexer owners.** A reward or commission withdrawal that takes the fallback
route emits `withdraw_addr_redirected`, with the attributes `original_withdraw_address` (the
address the delegator had set), `withdraw_address` (where the rewards went: the owner's address,
or the literal value `community_pool`), `validator` and, when a delegation is involved,
`delegator`. The chain's own indexer does not read it; an external indexer that sums
`withdraw_rewards` events should expect a withdrawal that credits the community pool rather than
an account.

**Our own `Delegate` calls are on the fallback rule.** `x/delegationrouter`'s
`MsgDelegateRouted` and the `x/stakeengine` queue fill call the staking keeper directly, not
through `MsgDelegate`, so they do not carry the strict-withdraw context. If the delegator's
withdraw address were a blocked address, the rewards withdrawn on the delegation would go to
the delegator, then to the community pool, where v0.53.6 failed the delegation. The case is not
reachable today: delegators are transaction signers, which a module account cannot be, and
`MsgSetWithdrawAddress` refuses a blocked address. The module-account set has not changed
since v0.9.1.

**A node whose clock runs slow can refuse a valid block.** The CometBFT block-time check is
local and uses the wall clock: a node whose clock is 60 s or more behind a block's time rejects
that block, in consensus and in blocksync. A synchronised clock (NTP) becomes a requirement for
validators and sentries. the deployment notes and `docs/MULTINODE-JOIN.md` state it with v0.10.0, and the
v0.10.0 runbook checks it (§0.1); `docs/RUN_A_NODE.md` takes it with the S9 operator instructions.
`block_time_tolerance` and `event_bus_buffer_capacity` are new `config.toml` keys; a `config.toml`
written by an older binary does not have them and the defaults apply.

**The license set does not change.** The third-party license collector passes on the new
module graph; the five modules that moved keep their licenses (Apache-2.0 for the SDK,
CometBFT and highwayhash, MIT for ibc-go v10 and lib/pq), and no new module is linked.

## Alternatives considered

**Move to release family 2026.1 now (SDK v0.55 / CometBFT v0.40 / ibc-go v11).** Planned as a
later slot, picked before family 2026.2 ships so that the move is not forced by an end-of-life
notice. It fixes nothing published that v0.53.8 does not, and it carries six blockers that each
need a decision or a design of their own:

- **x/group** leaves the Apache-2.0 SDK for `enterprise/group` under a source-available
  evaluation license that forbids production use. The chain wires x/group, so it is dropped,
  carried as our own Apache copy of the v0.53 module, or licensed.
- **x/params is removed** and ibc-go v11 drops `ParamSubspace` from its keeper constructors;
  `app/ibc.go` and the `ParamsKeeper` in `app/app.go` are rewritten, and the params store is
  deleted in the store upgrade.
- **SIGN_MODE_TEXTUAL is removed**; the textual metadata hook in `cmd/saicond/cmd/root.go`
  goes with it.
- **Consensus key rotation** is enabled on every upgrading chain. A rotation changes the
  consensus address a validator signs under, which four custom modules key on
  (participation scoring, compliance attribution, validator registry, stake engine) and which
  per-validator VRF depends on. Each handles rotation, or the ante chain refuses
  `MsgRotateConsPubKey`.
- **Go 1.26.5** becomes the toolchain (go.mod, the Docker image, CI, golangci-lint).
- **CometBFT 0.40 operations**: new `config.toml` sections, the sentry profiles of ADR-0015
  and the node profiles re-validated against them.

**Stay on v0.53.6.** Zero change to the release in flight. Rejected: it keeps a public DevNet
knowingly below a release its maintainers call a security fix, on the transaction path every
account can reach, and below CometBFT's evidence, blocksync and mempool hardening. Each month on
an ageing pin also widens the later move to 2026.1.

**Bump CometBFT alone.** Its patch releases do not change the state machine, so it could ship
without the upgrade height. Rejected because the upgrade height is already there: splitting the
SDK patch off would mean a second coordinated halt for no gain.

## Implementation (S8, October 2026)

`chain/go.mod` and `chain/go.sum` only; no source change was needed.

- `go build`, `go vet`, `go test` and `gofmt -l` are clean in `chain/` and `indexer/`;
  golangci-lint v2.12.2 reports no findings in either.
- The four simulations (full, import-export, non-determinism, after-import) pass.
- The app-level upgrade tests in `app/upgrade_test.go` pass, including the upgrade through a
  real block and the export and re-import of the post-upgrade state.
- `./scripts/rehearse-upgrade-v0.10.0.sh` with `OLD_REF=v0.9.1` passes in both modes, with the
  old binary built from the v0.9.1 tag and the new one from this tree. `SKIP_VRF=1` (the
  upgrade alone): halt at 34, the new binary to height 40. The full run (VRF key ceremony,
  10-block participation epochs): halt at 71, the new binary to height 354 (283 blocks,
  each tenth one closing a participation epoch), through the round 1 skip and the round 2
  draw. Every BeginBlock and EndBlock
  on the new binary, from the upgrade height on, completed.
- On the home the full run left behind, the new binary withdrew the rewards of the
  validator's self-delegation made under v0.9.1, delegated, withdrew, partially undelegated
  and withdrew everything, all in blocks, with no historical-rewards error in the node log.
  Those are the paths v0.53.8 made strict about the historical-rewards record, run against
  records the v0.9.1 binary wrote.

## References

- Cosmos SDK v0.53.8 release and changelog:
  <https://github.com/cosmos/cosmos-sdk/releases/tag/v0.53.8>,
  <https://github.com/cosmos/cosmos-sdk/blob/v0.53.8/CHANGELOG.md>
- CometBFT releases and changelog: <https://github.com/cometbft/cometbft/releases>,
  <https://github.com/cometbft/cometbft/blob/v0.38.25/CHANGELOG.md>
- ibc-go releases: <https://github.com/cosmos/ibc-go/releases>
- Release families and support policy: <https://docs.cosmos.network/sdk/latest/release-family>
- Upgrading to v0.55: <https://github.com/cosmos/cosmos-sdk/blob/v0.55.0/UPGRADING.md>
- [ADR-0002](ADR-0002-cosmos-sdk-tooling-version-pin.md) — the version pin and its upgrade
  procedure
