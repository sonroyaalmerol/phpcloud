# PHPCloud

A pure Go runtime shim for containerized PHP applications on Kubernetes. Replaces Apache/nginx+mod_php as the container entry point while providing horizontal scaling capabilities without modifying application code.

## Features

- **Zero PHP Changes**: Works with existing PHP applications as-is
- **Pure Go**: No CGO dependencies, single static binary (~25MB)
- **CRDT-Based Storage**: Ephemeral storage with automatic conflict resolution using Hybrid Logical Clocks
- **Gossip Clustering**: Peer discovery and leader election via memberlist
- **SQL Proxy**: Migration-aware read-only mode for zero-downtime database updates
- **Session Management**: Distributed session storage with CRDT replication
- **Prometheus Metrics**: Built-in observability with health/readiness checks

## Quick Start

### Docker

```bash
# Build
docker build -t phpcloud .

# Run with WordPress
docker run -p 8080:8080 \
  -e PHPCLOUD_PROFILE=generic \
  -e PHPCLOUD_PHP_FPM_SOCKET=tcp://wordpress:9000 \
  -e PHPCLOUD_SQL_PROXY_ENABLED=true \
  -e PHPCLOUD_SQL_PROXY_TARGET_HOST=mysql \
  phpcloud
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: php-app
spec:
  replicas: 3
  template:
    spec:
      containers:
        - name: phpcloud
          image: ghcr.io/sonroyaalmerol/phpcloud:latest
          ports:
            - containerPort: 8080
              name: http
            - containerPort: 7946
              name: gossip
          env:
            - name: PHPCLOUD_PROFILE
              value: generic
          readinessProbe:
            httpGet:
              path: /phpcloud/readyz
              port: 8080
          livenessProbe:
            httpGet:
              path: /phpcloud/healthz
              port: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: phpcloud-gossip
spec:
  clusterIP: None
  selector:
    app: php-app
  ports:
    - port: 7946
```

## Configuration

Configuration via YAML file or environment variables:

```yaml
# phpcloud.yaml
app_profile: generic

server:
  http_port: 8080
  gossip_port: 7946
  metrics_port: 9090

php_fpm:
  socket: tcp://php-fpm:9000
  external: true

sql_proxy:
  enabled: true
  listen_addr: "0.0.0.0:3307"
  target_host: "mysql"
  target_port: 3306

session:
  enabled: true
  backend: db
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `PHPCLOUD_CONFIG` | Config file path |
| `PHPCLOUD_LOG_LEVEL` | Log level (debug/info/warn/error) |
| `PHPCLOUD_NODE_NAME` | Node name for clustering |
| `PHPCLOUD_PROFILE` | App profile (generic) |

## Architecture

```
┌─────────────────────────────────────────┐
│           HTTP Requests                 │
└──────────────┬──────────────────────────┘
               │
       ┌───────▼────────┐
       │  PHPCloud      │
       │  HTTP Gateway  │
       └───────┬────────┘
               │
    ┌──────────┼──────────┐
    │          │          │
┌───▼───┐ ┌───▼───┐ ┌───▼────┐
│Static │ │Health │ │ FastCGI│
│Files  │ │Checks │ │ PHP-FPM│
└───────┘ └───────┘ └────┬───┘
                         │
              ┌──────────┼──────────┐
              │          │          │
        ┌─────▼──┐ ┌───▼────┐ ┌───▼────┐
        │Session │ │SQL     │ │CRDT    │
        │Storage │ │Proxy   │ │Storage │
        └────────┘ └────────┘ └────────┘
```

## Endpoints

- `/phpcloud/healthz` - Health check
- `/phpcloud/readyz` - Readiness check
- `/phpcloud/metrics` - Prometheus metrics (port 9090)

## Development

```bash
# Build
go build -o phpcloud ./cmd/phpcloud

# Test
go test -short ./...

# Run locally
./phpcloud --config phpcloud.yaml
```

## License

MIT
