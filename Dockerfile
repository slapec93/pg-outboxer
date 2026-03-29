FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X main.version=docker -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')" \
    -o pg-outboxer ./cmd/pg-outboxer

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/pg-outboxer .

EXPOSE 9090

ENTRYPOINT ["./pg-outboxer"]
CMD ["run", "--config=/config.yaml"]
