# SAiCoN

**Lite Paper**

*A short introduction to the SAiCoN protocol. The white paper is the authoritative
specification.*

---

## SAiCoN in one paragraph

SAiCoN is a Layer-1 blockchain where validators are paid for what they do, not for what they
hold. Every epoch the network measures each validator's participation — the blocks it signed,
the proposer turns it took — and pays only those that were active, compliant and actually
participated. Stake still admits a validator and weights its reward, but it does not decide who
is selected. Reputation does. The native token, SCN, has a fixed supply of 21 million enforced
by code before every mint.

## The problem

In ordinary proof-of-stake, capital compounds. The largest stakes earn the most, buy more, and
end up controlling a third of the network between ten operators. And on such a network, running
a validator well and merely running one pay exactly the same.

## The idea

Separate the two jobs stake does. **Stake admits you** — a self-bond of 2,000 SCN, locked for
60 days, so a real operator with real exposure stands behind every node. **Stake weights your
reward** once you are selected. But **participation decides whether you are selected**, and
participation is measured from the blockchain itself: signatures and proposer turns that every
node can verify.

## How a validator earns

1. **Register** with a 2,000 SCN self-bond. It is locked for 60 days. Leaving later takes the
   network's 21-day unbonding period, which cannot be shortened.

2. **Participate.** Each epoch produces a participation score from uptime and block quality.

3. **Build reputation.** The score is smoothed into a reputation that falls fast and recovers
   slowly. After a three-day outage, ten epochs of perfect participation are needed to become
   eligible again. Reputation is cheap to lose and expensive to regain — on purpose.

4. **Be selected.** The active set is designed for 75 seats, the final size fixed by a
   consensus benchmark. Seventy percent go to the highest reputations;
   thirty percent are drawn by a verifiable lottery among the rest, one ticket per verified
   operator. Stake plays no part in the ranking.

5. **Earn.** The epoch's reward is shared among selected validators that pass the gate, in
   proportion to voting power — capped stake times reputation.

## What keeps it decentralized

Limits, not discounts.

- No validator holds more than **2%** of bonded stake.
- No real-world operator holds more than **5%**, more than **two seats**, or more than **one
  lottery ticket** — however many nodes it runs.
- Above a global cap, new stake **waits in a queue**.
- A delegator splits across at most three validators, and overflow is routed away from
  concentration, toward smaller validators with room and a good record.

In simulation, the two-seat limit alone took a modelled attacker's maximum voting power from
53.8% to 3.2%.

Compliance is a gate, not a score — fail it and you are out, whatever else you signal. Energy
sourcing is recorded and published but carries no economic weight: every version that paid for
it was shown to invite paying for the certificate instead of the energy.

## The SCN token

| | |
|---|---|
| Supply ceiling | **21,000,000 SCN** — a constant in the code, checked before every mint |
| Who can mint | exactly one module; the standard inflation module is not in the application |
| Emission | a security budget: 15% of bonded stake per year, bounded to 0.5–0.75% of supply — about 238 SCN per day at the current network snapshot |
| Transaction fees | reduce new issuance before they add to rewards; at about 100,000 SCN of annual fees, minting stops |
| Precision | 8 decimals; base unit `nscn` |

The project's community holds an earlier token on the Base network today. After MainNet its
balances convert into SCN stake, under a rule published before genesis.

## Security

- **Double-signing** costs 10% of stake and a permanent ban of the key.
- **Ordinary downtime** costs reputation and reward, not stake — a hardware failure is not
  treated like fraud.
- **Cartels** are addressed first by attestation — every validator is linked to a verified
  operator before it can be selected — and second by a detector that can quarantine but never
  slash on its own.
- **Runtime invariants** halt the chain rather than let its accounting silently diverge.
- Governance is **bounded**: no vote can raise the supply ceiling, add a mint path, lower the
  lock-up, override slashing or clear a ban.

## Where it stands

**Running now, on a public developer network:** the four protocol modules, the stake limits and
the delegation router, the complete validator lifecycle, the participation gate, the single mint
path and supply ceiling, the emission budget, the runtime invariants, and a public explorer,
scoreboard and API.

**Next:** the entity registry, participation-based voting power, a public testnet, a consensus
benchmark and an external audit. Real payouts follow the audit, not precede it.

**Behind the numbers:** a programme of executed simulations — nineteen studies, every one
stating what it does not show.

## Get involved

The developer network is public — `explorer.saicon.world`, `api.saicon.world`,
`rpc.saicon.world`, chain `saicon-devnet-1`. Any Cosmos wallet connects. The invite DevNet for
operators opens in November 2026; the public testnet follows.
