# Configuration Guide

The Game Telemetry Collector is configured entirely through environment variables. This makes it easy to deploy in containerized environments and follows the [12-factor app](https://12factor.net/config) methodology.

## Configuration File

All configuration is loaded from environment variables. For local development, you can use the `.env` file:

1. Copy the template:
   ```bash
   cp .env.template .env
   ```

2. Edit `.env` with your configuration values

3. The application will automatically load variables from `.env` on startup

## Configuration Sections

### AccelByte Gaming Services

Required credentials for authentication:

```bash
AB_BASE_URL=https://test.accelbyte.io
AB_NAMESPACE=your-namespace
AB_CLIENT_ID=your-client-id
AB_CLIENT_SECRET=your-client-secret
PLUGIN_GRPC_SERVER_AUTH_ENABLED=true
```

### Server Settings

Controls gRPC and HTTP server behavior:

```bash
SERVER_GRPC_PORT=6565          # gRPC server port
SERVER_HTTP_PORT=8000          # HTTP/REST API port
SERVER_BASE_PATH=/telemetry    # API base path
SERVER_LOG_LEVEL=info          # log level: debug, info, warn, error
```

### Processor Settings

Configures the async event processing pipeline:

```bash
PROCESSOR_WORKERS=10                      # Number of worker goroutines
PROCESSOR_CHANNEL_BUFFER=10000           # Event queue size
PROCESSOR_DEFAULT_BATCH_SIZE=100         # Default batch size
PROCESSOR_DEFAULT_FLUSH_INTERVAL=5s      # Default flush interval
```

### Deduplication

Prevents duplicate event processing:

```bash
DEDUP_ENABLED=false               # Enable/disable deduplication
DEDUP_TYPE=noop                   # Type: memory, redis, or noop
DEDUP_TTL=1h                      # How long to remember events

# Redis configuration (when DEDUP_TYPE=redis)
DEDUP_REDIS_ADDR=localhost:6379
DEDUP_REDIS_PASSWORD=
DEDUP_REDIS_DB=0
```

**Deduplication Types:**
- `noop`: Disabled (no deduplication)
- `memory`: In-memory (single instance only)
- `redis`: Distributed (works across multiple instances)

### Storage Plugins

Specify which storage backends to use:

```bash
# Comma-separated list of enabled plugins
STORAGE_ENABLED_PLUGINS=noop
# Examples:
# STORAGE_ENABLED_PLUGINS=postgres,s3
# STORAGE_ENABLED_PLUGINS=kafka,mongodb
```

#### No-op Plugin

Discards all events (useful for testing):

```bash
STORAGE_NOOP_BATCH_SIZE=100
STORAGE_NOOP_FLUSH_INTERVAL=5s
STORAGE_NOOP_WORKERS=2
STORAGE_NOOP_RETRY_MAX_RETRIES=3
STORAGE_NOOP_RETRY_INITIAL_BACKOFF=100ms
STORAGE_NOOP_RETRY_MAX_BACKOFF=10s
STORAGE_NOOP_RETRY_MULTIPLIER=2.0
```

#### PostgreSQL Plugin

Stores events in PostgreSQL database:

```bash
STORAGE_POSTGRES_BATCH_SIZE=200
STORAGE_POSTGRES_FLUSH_INTERVAL=5s
STORAGE_POSTGRES_WORKERS=8
STORAGE_POSTGRES_RETRY_MAX_RETRIES=3
STORAGE_POSTGRES_RETRY_INITIAL_BACKOFF=200ms
STORAGE_POSTGRES_RETRY_MAX_BACKOFF=10s
STORAGE_POSTGRES_RETRY_MULTIPLIER=2.0
STORAGE_POSTGRES_DSN=postgres://user:pass@localhost:5432/telemetry?sslmode=disable
STORAGE_POSTGRES_TABLE=telemetry_events
```

#### S3 Plugin

Stores events as JSON files in S3:

```bash
STORAGE_S3_BATCH_SIZE=500
STORAGE_S3_FLUSH_INTERVAL=10s
STORAGE_S3_WORKERS=5
STORAGE_S3_RETRY_MAX_RETRIES=5
STORAGE_S3_RETRY_INITIAL_BACKOFF=500ms
STORAGE_S3_RETRY_MAX_BACKOFF=30s
STORAGE_S3_RETRY_MULTIPLIER=2.0
STORAGE_S3_BUCKET=game-telemetry-prod
STORAGE_S3_PREFIX=telemetry
STORAGE_S3_REGION=us-east-1
```

For S3, you also need AWS credentials:
```bash
AWS_ACCESS_KEY_ID=your-key
AWS_SECRET_ACCESS_KEY=your-secret
# OR use IAM role (recommended in AWS)
```

#### Kafka Plugin

Streams events to Kafka:

```bash
STORAGE_KAFKA_BATCH_SIZE=1000
STORAGE_KAFKA_FLUSH_INTERVAL=1s
STORAGE_KAFKA_WORKERS=3
STORAGE_KAFKA_RETRY_MAX_RETRIES=10
STORAGE_KAFKA_RETRY_INITIAL_BACKOFF=100ms
STORAGE_KAFKA_RETRY_MAX_BACKOFF=5s
STORAGE_KAFKA_RETRY_MULTIPLIER=1.5
STORAGE_KAFKA_BROKERS=localhost:9092
STORAGE_KAFKA_TOPIC=game-telemetry
STORAGE_KAFKA_COMPRESSION=snappy
```

For multiple Kafka brokers, use comma-separated list:
```bash
STORAGE_KAFKA_BROKERS=kafka1:9092,kafka2:9092,kafka3:9092
```

#### MongoDB Plugin

Stores events in MongoDB:

```bash
STORAGE_MONGODB_BATCH_SIZE=500
STORAGE_MONGODB_FLUSH_INTERVAL=5s
STORAGE_MONGODB_WORKERS=5
STORAGE_MONGODB_RETRY_MAX_RETRIES=3
STORAGE_MONGODB_RETRY_INITIAL_BACKOFF=200ms
STORAGE_MONGODB_RETRY_MAX_BACKOFF=10s
STORAGE_MONGODB_RETRY_MULTIPLIER=2.0
STORAGE_MONGODB_URI=mongodb://localhost:27017
STORAGE_MONGODB_DATABASE=telemetry
STORAGE_MONGODB_COLLECTION=events
```

## Configuration Tips

### Multiple Storage Backends

You can enable multiple storage plugins simultaneously. Events will be sent to all enabled plugins:

```bash
# Store in both PostgreSQL and S3
STORAGE_ENABLED_PLUGINS=postgres,s3

# Stream to Kafka and archive in S3
STORAGE_ENABLED_PLUGINS=kafka,s3

# Store in all available backends
STORAGE_ENABLED_PLUGINS=postgres,s3,kafka,mongodb
```

### Performance Tuning

**High Throughput:**
```bash
PROCESSOR_WORKERS=20
PROCESSOR_CHANNEL_BUFFER=50000
STORAGE_POSTGRES_BATCH_SIZE=500
STORAGE_POSTGRES_WORKERS=16
```

**Low Latency:**
```bash
PROCESSOR_WORKERS=5
PROCESSOR_CHANNEL_BUFFER=1000
STORAGE_KAFKA_BATCH_SIZE=100
STORAGE_KAFKA_FLUSH_INTERVAL=500ms
```

**Resource Constrained:**
```bash
PROCESSOR_WORKERS=4
PROCESSOR_CHANNEL_BUFFER=5000
STORAGE_POSTGRES_WORKERS=4
STORAGE_POSTGRES_BATCH_SIZE=100
```

### Production Recommendations

1. **Enable Deduplication with Redis:**
   ```bash
   DEDUP_ENABLED=true
   DEDUP_TYPE=redis
   DEDUP_REDIS_ADDR=redis-cluster:6379
   ```

2. **Use Multiple Storage Backends:**
   ```bash
   STORAGE_ENABLED_PLUGINS=postgres,s3
   ```

3. **Configure Retry Policies:**
   - Higher retries for critical storage (PostgreSQL)
   - Lower retries for optional storage (S3)

4. **Monitor Resource Usage:**
   - Adjust worker count based on CPU cores
   - Adjust channel buffer based on memory availability

## Environment-Specific Configuration

### Development
```bash
STORAGE_ENABLED_PLUGINS=noop
DEDUP_ENABLED=false
SERVER_LOG_LEVEL=debug
```

### Staging
```bash
STORAGE_ENABLED_PLUGINS=postgres
DEDUP_ENABLED=true
DEDUP_TYPE=redis
SERVER_LOG_LEVEL=info
```

### Production
```bash
STORAGE_ENABLED_PLUGINS=postgres,s3,kafka
DEDUP_ENABLED=true
DEDUP_TYPE=redis
SERVER_LOG_LEVEL=warn
PROCESSOR_WORKERS=20
```

## Docker Compose Example

See `docker-compose.yaml` for a complete local development setup with PostgreSQL, Redis, and MinIO (S3-compatible storage).

## Kubernetes/Cloud Deployment

Store sensitive values in secrets management:
- Kubernetes Secrets
- AWS Secrets Manager
- Azure Key Vault
- GCP Secret Manager

Then reference them in your deployment configuration.
