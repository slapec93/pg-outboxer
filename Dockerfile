FROM golang:1.26-alpine AS builder

WORKDIR /build

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

RUN apk --no-cache add ca-certificates postgresql-client

WORKDIR /app

COPY --from=builder /build/pg-outboxer .

# Copy embedded migrations (if needed for setup command)
COPY --from=builder /build/cmd/pg-outboxer/migrations ./migrations

EXPOSE 9090

ENTRYPOINT ["./pg-outboxer"]
CMD ["run", "--config", "/app/config.yaml"]
