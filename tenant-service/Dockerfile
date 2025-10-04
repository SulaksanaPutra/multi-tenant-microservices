# Stage 1: Build binary
FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/tenant-service ./cmd

# Stage 2: Runtime environment
FROM alpine:latest

RUN apk --no-cache add ca-certificates curl

WORKDIR /root/
COPY --from=builder /app/tenant-service .
COPY --from=builder /app/migrations ./migrations

CMD ["./tenant-service"]
