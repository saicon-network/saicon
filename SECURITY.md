# Security policy

## Reporting a vulnerability

Please report security problems **privately** to **security@saicon.world**. Do not open a public
issue, pull request or discussion for a vulnerability, and do not post details anywhere public
until we have fixed it and told you we are ready.

Include what you can: the version or commit, the component, steps to reproduce, and what an
attacker would gain. A proof of concept helps. We will acknowledge your report, keep you
informed while we investigate, and credit you in the release notes if you wish.

## Scope

In scope:

- the chain: `chain/` (the `saicond` node, the custom modules in `chain/x/`, the app wiring and
  ante handlers, the genesis and consensus parameters we ship);
- the indexer: `indexer/`.

Of particular interest: consensus failures and halts, anything that moves or mints funds outside
the intended path, ways around the stake caps, lock-up or slashing rules, and invariant violations.

Out of scope: the block explorer fork and the static site (report issues there as ordinary bugs),
vulnerabilities in third-party dependencies that are not exploitable through SAiCoN (report those
upstream), denial of service by raw traffic volume, and social engineering.

## Supported versions

SAiCoN is on a DevNet. Only the latest release is supported, and the network may be reset from a
fresh genesis between releases. Nothing on the DevNet has monetary value.
