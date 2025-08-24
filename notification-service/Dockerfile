# Stage 1: Build binary
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/notification-service ./cmd/main.go

# Stage 2: Runtime environment
FROM alpine:latest

RUN apk --no-cache add ca-certificates curl

WORKDIR /root/
COPY --from=builder /app/notification-service .

CMD ["./notification-service"]
