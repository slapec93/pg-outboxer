FROM golang:1.26-alpine AS builder

WORKDIR /build

# Copy just the demo service
COPY examples/demo-service/main.go .

# Initialize module and build
RUN go mod init demo-service && \
    CGO_ENABLED=0 GOOS=linux go build -o webhook-receiver .

FROM alpine:latest

WORKDIR /app

COPY --from=builder /build/webhook-receiver .

EXPOSE 8080

CMD ["./webhook-receiver"]
