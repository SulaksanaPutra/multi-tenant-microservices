FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o infra-provisioner ./cmd/main.go

# Scratch / Distroless Minimal Image (Zero Shell, Zero OS Utilities, Non-Root)
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

COPY --from=builder /app/infra-provisioner /infra-provisioner

USER nonroot:nonroot

ENTRYPOINT ["/infra-provisioner"]
