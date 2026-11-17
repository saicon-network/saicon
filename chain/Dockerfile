# syntax=docker/dockerfile:1

############################
# Builder stage
############################
FROM golang:1.25-alpine AS builder

# Pure-Go build (CGO disabled, goleveldb backend). No build-base/wasm needed.
ENV CGO_ENABLED=0 \
    GOOS=linux

WORKDIR /src

# Cache module downloads first.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source.
COPY . .

# These mirror chain/Makefile APPNAME. They are cosmetic metadata only — the node
# home directory comes from app.Name ("saicon"), not from here — but they are what
# `saicond version` and any explorer's "Application Versions" panel display, so
# leaving them as the scaffold's "chain"/"chaind" showed the wrong product name on
# the public dashboard.
# Version/Commit are best-effort for a Docker build (no .git in context).
ARG VERSION=dev
ARG COMMIT=unknown
RUN go build \
    -ldflags "-X github.com/cosmos/cosmos-sdk/version.Name=saicon \
              -X github.com/cosmos/cosmos-sdk/version.AppName=saicond \
              -X github.com/cosmos/cosmos-sdk/version.Version=${VERSION} \
              -X github.com/cosmos/cosmos-sdk/version.Commit=${COMMIT}" \
    -o /out/saicond ./cmd/saicond

# The licenses of everything linked into saicond, and the source of the MPL / LGPL
# packages among it (./scripts/third-party-licenses). Fails the build on a module
# whose terms are missing or not recognised.
RUN go run ./scripts/third-party-licenses -out /out/licenses -goos linux -goarch "$(go env GOARCH)" -ref "${VERSION}"

# Cosmovisor runs saicond and, at the height of a governance upgrade, switches it to the
# release the upgrade plan names (docker/entrypoint-singlenode.sh). Built here, pure Go
# like saicond, in a throw-away module that requires it at the pinned version; its
# licenses are described by the same tool, pointed at its own source.
ARG COSMOVISOR_VERSION=v1.7.3
RUN go build -o /tmp/third-party-licenses ./scripts/third-party-licenses \
    && mkdir /tmp/cosmovisor && cd /tmp/cosmovisor \
    && go mod init saicon.local/cosmovisor-build \
    && go get "cosmossdk.io/tools/cosmovisor@${COSMOVISOR_VERSION}" \
    && go build -o /out/cosmovisor cosmossdk.io/tools/cosmovisor/cmd/cosmovisor \
    && /tmp/third-party-licenses -out /out/licenses-cosmovisor \
         -pkg cosmossdk.io/tools/cosmovisor/cmd/cosmovisor -goos linux -goarch "$(go env GOARCH)" \
         -name Cosmovisor -source https://github.com/cosmos/cosmos-sdk \
         -ref "tools/cosmovisor/${COSMOVISOR_VERSION}" -rebuild "go build ./tools/cosmovisor/cmd/cosmovisor"

############################
# Runtime stage
############################
FROM alpine:3.20

# apk upgrade first: the base image's own packages can lag the release branch, and the
# upgrade carries the branch's fixes. docker/alpine-sources.sh finds the recipe of each
# installed version in the branch history either way.
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates bash jq \
    && adduser -D -u 1000 saicon

COPY --from=builder /out/saicond /usr/bin/saicond
COPY --from=builder /out/licenses /usr/share/licenses/saicon
COPY --from=builder /out/cosmovisor /usr/bin/cosmovisor
COPY --from=builder /out/licenses-cosmovisor /usr/share/licenses/cosmovisor
COPY docker/IMAGE-LICENSES.txt /usr/share/licenses/saicon/README.txt
RUN apk list --installed > /usr/share/licenses/saicon/alpine-packages.txt
COPY docker/entrypoint-singlenode.sh /usr/local/bin/entrypoint-singlenode.sh
COPY ./scripts/vrf-submit-loop.sh /usr/local/bin/vrf-submit-loop.sh
RUN chmod +x /usr/local/bin/entrypoint-singlenode.sh /usr/local/bin/vrf-submit-loop.sh

# Pre-create the node home owned by the runtime user. A fresh named volume
# mounted here inherits this ownership, so the non-root user can write to it.
RUN mkdir -p /home/saicon/.saicon && chown -R saicon:saicon /home/saicon

USER saicon
WORKDIR /home/saicon

# p2p, rpc, rest (api), grpc
EXPOSE 26656 26657 1317 9090

ENTRYPOINT ["saicond"]
