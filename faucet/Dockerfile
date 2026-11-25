# Build context is the repo root (like the indexer's).
#
# saicond is NOT built from this checkout; it is copied out of the
# running node container into faucet/saicond-bin/ before the build (the binary
# Cosmovisor actually runs after an upgrade, else the container's /usr/bin/saicond),
# so the faucet's CLI is the one the chain runs. For a manual build:
#   docker cp -L saicon-node:/home/saicon/.saicon/cosmovisor/current/bin/saicond faucet/saicond-bin/saicond
#   docker build -f faucet/Dockerfile .
FROM golang:1.25-alpine AS build
ENV CGO_ENABLED=0
WORKDIR /src
COPY faucet/go.mod faucet/go.sum ./faucet/
RUN cd faucet && go mod download
COPY faucet/ ./faucet/
RUN cd faucet && go build -trimpath -o /out/faucet ./cmd/faucet

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 faucet \
 && mkdir -p /data && chown faucet:faucet /data
# /data must belong to the faucet user in the image so a fresh named volume is
# seeded writable (same reason as the indexer's Dockerfile).
COPY --from=build /out/faucet /usr/local/bin/faucet
COPY faucet/saicond-bin/saicond /usr/local/bin/saicond
USER faucet
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/faucet"]
