# Multi-stage build for the split-flap-display-web Go backend.
# Build context should be the repository root:
#   docker build -f backend-go/Dockerfile -t split-flap-web:latest .
#
# Or via docker-compose (which sets context=root automatically).

# --- Build stage --------------------------------------------------------
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Cache dependencies.
COPY backend-go/go.mod backend-go/go.sum ./
RUN go mod download

# Copy the rest of the Go module.
COPY backend-go/ ./

# Build a trimmed, dependency-only static binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/server \
    ./cmd/server

# --- Runtime stage ------------------------------------------------------
FROM alpine:3.20

# Install wget for HEALTHCHECK (tiny footprint).
RUN apk add --no-cache wget ca-certificates && \
    addgroup -S app && \
    adduser -S -G app -h /app -s /sbin/nologin app

WORKDIR /app

# Copy the binary and the static frontend.
COPY --from=builder /out/server        /app/server
COPY frontend/static                   /app/frontend/static

# Copy whichever config file is available: prefer backend-go/app.conf
# (per-environment override), fall back to the committed example.
COPY backend-go/app.conf*              /tmp/conf/
RUN if [ -f /tmp/conf/app.conf ]; then \
        cp /tmp/conf/app.conf        /app/app.conf; \
    elif [ -f /tmp/conf/app.conf.example ]; then \
        cp /tmp/conf/app.conf.example /app/app.conf; \
        cp /tmp/conf/app.conf.example /app/app.conf.example; \
    else \
        touch /app/app.conf; \
    fi && rm -rf /tmp/conf

USER app

EXPOSE 8100

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8100/api/config || exit 1

ENTRYPOINT ["/app/server"]