FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY main.go index.html ./

RUN CGO_ENABLED=0 GOOS=linux go build -o web-ui main.go

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/web-ui .

EXPOSE 3000

CMD ["./web-ui"]
