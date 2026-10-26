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

############################
# Runtime stage
############################
FROM alpine:3.20

# apk upgrade first: the base image's own packages can lag the release branch, and
# bringing every package to the branch head is what lets docker/alpine-sources.sh
# match each installed version to its recipe (and carries the branch's fixes).
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates bash jq \
    && adduser -D -u 1000 saicon

COPY --from=builder /out/saicond /usr/bin/saicond
COPY --from=builder /out/licenses /usr/share/licenses/saicon
COPY docker/IMAGE-LICENSES.txt /usr/share/licenses/saicon/README.txt
RUN apk list --installed > /usr/share/licenses/saicon/alpine-packages.txt
COPY docker/entrypoint-singlenode.sh /usr/local/bin/entrypoint-singlenode.sh
RUN chmod +x /usr/local/bin/entrypoint-singlenode.sh

# Pre-create the node home owned by the runtime user. A fresh named volume
# mounted here inherits this ownership, so the non-root user can write to it.
RUN mkdir -p /home/saicon/.saicon && chown -R saicon:saicon /home/saicon

USER saicon
WORKDIR /home/saicon

# p2p, rpc, rest (api), grpc
EXPOSE 26656 26657 1317 9090

ENTRYPOINT ["saicond"]
