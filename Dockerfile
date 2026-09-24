# Build the Mini App and admin UI without adding Node to the production image.
FROM node:20-alpine AS frontend
WORKDIR /ui
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run lint && npm run typecheck && npm run build && npm run build:admin

# Build stage
FROM golang:1.25-alpine AS builder

# Build arguments for versioning
ARG VERSION=prod
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown

WORKDIR /app

# Install build dependencies
# - gcc, musl-dev: Required for CGO/SQLite
RUN apk add --no-cache gcc musl-dev

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .
COPY --from=frontend /internal/web/miniapp_dist/ ./internal/web/miniapp_dist/
COPY --from=frontend /internal/web/admin_dist/ ./internal/web/admin_dist/

# Ensure go.mod is consistent with dependencies
RUN go mod tidy

# Build with optimizations:
# - CGO_ENABLED=1: Required for SQLite (mattn/go-sqlite3)
# - trimpath: Remove file system paths for reproducible builds
# - ldflags: Strip debug info (-s) and DWARF (-w), inject version info
RUN CGO_ENABLED=1 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT_SHA} \
        -X main.buildTime=${BUILD_TIME}" \
    -o rs8kvn_bot ./cmd/bot

# Runtime stage - minimal Alpine for production
FROM alpine:3.21

# Install runtime dependencies only
# - ca-certificates: HTTPS connections
# - procps: Process utilities (pgrep for healthcheck)
RUN apk add --no-cache ca-certificates procps && \
    rm -rf /var/cache/apk/*

# Create non-root user for security (never run as root)
RUN adduser -D -g '' appuser

# Create app directory with proper permissions
WORKDIR /app
RUN mkdir -p /app/data && chown -R appuser:appuser /app/data

# Copy binary from builder stage
COPY --from=builder --chown=appuser:appuser /app/rs8kvn_bot .

# Copy database migrations for runtime application
COPY --chown=appuser:appuser internal/database/migrations internal/database/migrations

# Switch to non-root user
USER appuser

# Memory optimization environment variables:
# GOMEMLIMIT: Soft memory limit for GC (64MB = 67108864 bytes)
# GOGC: Garbage collection frequency (lower = more aggressive, default 100)
# These settings optimize for low memory footprint
ENV GOMEMLIMIT=67108864
ENV GOGC=40

# Health check port
EXPOSE 8880

# Health check - verify the application and database are healthy
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8880/healthz > /dev/null

# Run the bot
CMD ["/app/rs8kvn_bot"]
