# Pure-Go build: the monorepo is CGO-free everywhere, and modernc.org/sqlite is
# chosen precisely so that stays true.
FROM golang:1.25-alpine AS build
ENV CGO_ENABLED=0
RUN apk add --no-cache git
WORKDIR /src

# The module has a replace directive to ../chain, so both must be in context.
COPY chain/go.mod chain/go.sum ./chain/
COPY indexer/go.mod indexer/go.sum ./indexer/
RUN cd indexer && go mod download

COPY chain/ ./chain/
COPY indexer/ ./indexer/
RUN cd indexer && go build -trimpath -o /out/indexer ./cmd/indexer

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 indexer \
 && mkdir -p /data && chown indexer:indexer /data
# /data must exist in the image and belong to the indexer user: Docker seeds a new,
# empty named volume with the ownership of the image's directory. Without it the
# volume is root-owned and the indexer cannot create its database — SQLite reports
# "unable to open database file" (code 14) and the container restarts forever.
COPY --from=build /out/indexer /usr/local/bin/indexer
USER indexer
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/indexer"]
