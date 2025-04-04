# Build stage
FROM golang:1.26-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o phpcloud ./cmd/phpcloud

# Final stage
FROM php:8.2-fpm-alpine

# Install PHP extensions and dependencies
RUN apk add --no-cache \
    ca-certificates \
    curl \
    libpq \
    postgresql-client \
    mysql-client \
    && rm -rf /var/cache/apk/*

# Copy the binary from builder
COPY --from=builder /build/phpcloud /phpcloud/phpcloud

# Copy profiles
COPY --from=builder /build/profiles /phpcloud/profiles

# Create necessary directories
RUN mkdir -p /phpcloud/migrations /run/php

# Set permissions
RUN chmod +x /phpcloud/phpcloud

# Create non-root user
RUN adduser -D -H -u 1000 phpcloud

# Expose ports
EXPOSE 8080 7946 9090

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/phpcloud/healthz || exit 1

# Set working directory
WORKDIR /var/www/html

# Entry point
ENTRYPOINT ["/phpcloud/phpcloud"]
CMD ["--config", "/phpcloud/phpcloud.yaml"]
