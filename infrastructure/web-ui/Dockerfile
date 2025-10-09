FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod ./
COPY main.go ./
COPY public/ ./public/

RUN CGO_ENABLED=0 GOOS=linux go build -o web-ui main.go

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/web-ui .

EXPOSE 3000

CMD ["./web-ui"]
