# Contributing to SAiCoN

Thank you for looking. A few things to know first.

## This repository is a release mirror

SAiCoN is developed in a main repository; this one carries each release as a single commit. A pull
request opened here is welcome and will be read and reviewed, but it lands through the main
repository and appears here in the next release, not as a merge on this page. For anything larger
than a small fix, open an issue first so we can agree on the shape of the change.

Vulnerabilities do not go in issues or pull requests. See [SECURITY.md](SECURITY.md).

## Build and test

You need Go 1.25 (the exact version is in `chain/go.mod`) and, for the demos, Docker.

```bash
cd chain
go build ./...
go vet ./...
go test ./...

cd ../indexer
go build ./... && go vet ./... && go test ./...
```

Protobuf sources are in `chain/proto/`; regenerate Go code with `make proto-gen` from `chain/`
(see the Makefile for the tools it needs). Do not edit generated `*.pb.go` files by hand.

## Demos

Every claim in the docs can be reproduced by a script in `chain/scripts/`:
`demo-pop-cycle.sh`, `demo-stakeengine.sh`, `demo-delegationrouter.sh`, `rehearse-join.sh`. They
start containers from the local build and narrate each step. If you change behaviour, run the
matching demo and the `test-*.sh` script next to it.

## Changes

- Keep a change to one concern, with tests next to the code it touches.
- Any change to consensus, staking, rewards or the stake caps needs a test that fails without it
  and a note in `chain/CHANGELOG.md` under *Unreleased*.
- A decision that changes the design belongs in an ADR in `chain/docs/adr/`.
- Documentation is English; the papers also have Chinese translations. If you change a paper, say which sections of the translation need a second look.

## Commit style

A short imperative subject (about 70 characters), a blank line, then a body that says why, not
only what. A type prefix is welcome: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`.

```
fix: refuse a routed delegation when the queue is full

The router computed the plan before checking the queue length, so a full queue
failed late with a confusing error. Check first and name the limit.
```

## Conduct

Everyone taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
