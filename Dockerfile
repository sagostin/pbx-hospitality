# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bicom-hospitality ./cmd/bicom-hospitality

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /bicom-hospitality .

# Use non-root user
USER appuser

# Expose API port
EXPOSE 8080

# Health check — uses the binary's own --health-check flag which validates
# config + DB connectivity without booting the full HTTP server.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["./bicom-hospitality", "--health-check"] || exit 1

ENTRYPOINT ["./bicom-hospitality"]
