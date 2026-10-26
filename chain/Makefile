BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
COMMIT := $(shell git log -1 --format='%H')
APPNAME := saicon

# do not override user values
ifeq (,$(VERSION))
  VERSION := $(shell git describe --exact-match 2>/dev/null)
  # if VERSION is empty, then populate it with branch name and raw commit hash
  ifeq (,$(VERSION))
    VERSION := $(BRANCH)-$(COMMIT)
  endif
endif

# Update the ldflags with the app, client & server names
ldflags = -X github.com/cosmos/cosmos-sdk/version.Name=$(APPNAME) \
	-X github.com/cosmos/cosmos-sdk/version.AppName=$(APPNAME)d \
	-X github.com/cosmos/cosmos-sdk/version.Version=$(VERSION) \
	-X github.com/cosmos/cosmos-sdk/version.Commit=$(COMMIT)

BUILD_FLAGS := -ldflags '$(ldflags)'

##############
###  Test  ###
##############

test-unit:
	@echo Running unit tests...
	@go test -mod=readonly -v -timeout 30m ./...

test-race:
	@echo Running unit tests with race condition reporting...
	@go test -mod=readonly -v -race -timeout 30m ./...

test-cover:
	@echo Running unit tests and creating coverage report...
	@go test -mod=readonly -v -timeout 30m -coverprofile=$(COVER_FILE) -covermode=atomic ./...
	@go tool cover -html=$(COVER_FILE) -o $(COVER_HTML_FILE)
	@rm $(COVER_FILE)

bench:
	@echo Running unit tests with benchmarking...
	@go test -mod=readonly -v -timeout 30m -bench=. ./...

test: govet govulncheck test-unit

# Simulation. All four already existed in app/sim_test.go and had never run:
# they skip unless -Enabled=true is passed, so `go test ./...` reports them green
# having executed nothing. These targets give them a way to be run on purpose.
#
# Deliberately NOT part of `make test` or the CI gate — a simulation takes minutes
# to hours and its failures need reading, not a red pipeline. Run them before a
# release and before booking an audit.
#
# Every target goes through ./scripts/run-sim.sh, which fails on a SKIP. `go test`
# exits 0 when a test skips, so a simulation that never ran is indistinguishable
# from one that passed — see the script for the two times that bit this suite.
SIM_NUM_BLOCKS ?= 50
SIM_BLOCK_SIZE ?= 100
SIM_SEED       ?= 42
# The determinism simulation needs its OWN seed, and it must never be 42.
# simcli.DefaultSeedValue IS 42, and TestAppStateDeterminism reads "the seed equals
# the default" as "draw a random one" (app/sim_test.go, the numSeeds branch). So
# SIM_SEED does not pin that test — it randomises it, and the gate's outcome depends
# on the draw. Measured 2026-08-24: one draw emptied the validator set and the test
# skipped; the next passed. A pinned non-default seed prints once instead of three
# times, which is how you can see it took.
SIM_DET_SEED   ?= 20260824
# -Params points at app/sim_params.json — the path is relative to the test's own
# directory. It zeroes five staking operation weights. Three because the SDK
# picks the operator's own delegation to undelegate or redelegate and the V-3
# lock-up refuses it (c4ad849); two since S6 because the simulator creates
# validators below the 2,000 SCN entry floor and delegates past the 2% cap, which
# the stake-caps ante refuses. Correct refusals, and a real coverage gap until the
# simulator has operations that know the rules.
SIM_FLAGS = -Enabled=true -NumBlocks=$(SIM_NUM_BLOCKS) -BlockSize=$(SIM_BLOCK_SIZE) \
            -Commit=true -Period=1 -Params=sim_params.json -v -timeout 24h

test-sim-full:
	@echo "Running the full application simulation..."
	@./scripts/run-sim.sh TestFullAppSimulation $(SIM_SEED) $(SIM_FLAGS)

test-sim-nondeterminism:
	@echo "Checking the app state is deterministic across identical runs..."
	@./scripts/run-sim.sh TestAppStateDeterminism $(SIM_DET_SEED) $(SIM_FLAGS)

test-sim-import-export:
	@echo "Exporting simulated state and importing it into a fresh app..."
	@./scripts/run-sim.sh TestAppImportExport $(SIM_SEED) $(SIM_FLAGS)

test-sim-after-import:
	@echo "Continuing the simulation on an imported state..."
	@./scripts/run-sim.sh TestAppSimulationAfterImport $(SIM_SEED) $(SIM_FLAGS)

test-sim: test-sim-nondeterminism test-sim-full test-sim-import-export test-sim-after-import

.PHONY: test test-unit test-race test-cover bench \
        test-sim test-sim-full test-sim-nondeterminism test-sim-import-export test-sim-after-import

#################
###  Install  ###
#################

all: install

install:
	@echo "--> ensure dependencies have not been modified"
	@go mod verify
	@echo "--> installing $(APPNAME)d"
	@go install $(BUILD_FLAGS) -mod=readonly ./cmd/$(APPNAME)d

.PHONY: all install

##################
###  Protobuf  ###
##################

# Use this target if you do not want to use Ignite for generating proto files

proto-deps:
	@echo "Installing proto deps"
	@echo "Proto deps present, run 'go tool' to see them"

proto-gen:
	@echo "Generating protobuf files..."
	@ignite generate proto-go --yes

.PHONY: proto-gen

#################
###  Linting  ###
#################

lint:
	@echo "--> Running linter"
	@go tool github.com/golangci/golangci-lint/cmd/golangci-lint run ./... --timeout 15m

lint-fix:
	@echo "--> Running linter and fixing issues"
	@go tool github.com/golangci/golangci-lint/cmd/golangci-lint run ./... --fix --timeout 15m

.PHONY: lint lint-fix

###################
### Development ###
###################

govet:
	@echo Running go vet...
	@go vet ./...

govulncheck:
	@echo Running govulncheck...
	@go tool golang.org/x/vuln/cmd/govulncheck@latest
	@govulncheck ./...

.PHONY: govet govulncheck