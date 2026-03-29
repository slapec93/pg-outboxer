FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY main.go .
RUN go mod init webhook-receiver && go mod tidy
RUN go build -o webhook-receiver .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/webhook-receiver .

EXPOSE 8081
CMD ["./webhook-receiver"]
