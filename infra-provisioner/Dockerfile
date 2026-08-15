FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o infra-provisioner ./cmd/main.go

# Runtime image is postgres:16-alpine so pg_dump/psql/sed are available for
# the shared <-> dedicated data migration pipelines. The postgres entrypoint
# is overridden below; only the client binaries are used.
FROM postgres:16-alpine

WORKDIR /

COPY --from=builder /app/infra-provisioner /infra-provisioner

USER root:root

ENTRYPOINT ["/infra-provisioner"]
