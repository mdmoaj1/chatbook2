FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app

# Download dependencies (cached layer)
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=${BUILD_VERSION}" \
    -o chatbook-server \
    ./cmd/server

# ── Runtime image (minimal) ──────────────────────────────────────────────────
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /app/chatbook-server /chatbook-server
COPY --from=builder /app/migrations /migrations

EXPOSE 6060

USER 65534:65534

ENTRYPOINT ["/chatbook-server"]
