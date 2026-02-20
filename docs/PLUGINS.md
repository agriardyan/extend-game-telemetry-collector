# Storage Plugins Guide

This document provides detailed information about the available storage plugins and how to use them.

## Available Plugins

### 1. No-op Plugin
**Purpose**: Testing and development
**Package**: `pkg/storage/plugins/noop`

The simplest plugin that accepts all events but doesn't store them. Useful for:
- Testing the processor without storage overhead
- Measuring API throughput
- Development and debugging

**Configuration**:
```yaml
- name: noop
  enabled: true
  batch_size: 100
  flush_interval: 5s
  workers: 2
  settings: {}
```

---

### 2. Amazon S3 Plugin
**Purpose**: Long-term storage and data lake
**Package**: `pkg/storage/plugins/s3`

Stores telemetry events as JSON files in Amazon S3 with automatic partitioning.

**Features**:
- ✅ Automatic partitioning by namespace, year, month, and day
- ✅ JSON format for easy querying with Athena/Glue
- ✅ Batch writes to minimize API calls
- ✅ Metadata tags for better organization
- ✅ Health checks for bucket accessibility

**File Structure**:
```
s3://bucket-name/
  └── telemetry/
      └── my-game/
          └── year=2026/
              └── month=02/
                  └── day=16/
                      ├── 1708084800000.json
                      ├── 1708084805000.json
                      └── ...
```

**Configuration**:
```yaml
- name: s3
  enabled: true
  batch_size: 500           # Events per file
  flush_interval: 10s
  workers: 5
  retry:
    max_retries: 5
    initial_backoff: 500ms
    max_backoff: 30s
    multiplier: 2.0
  settings:
    bucket: game-telemetry-prod
    prefix: telemetry
    region: us-east-1
```

**Environment Setup**:
```bash
# Option 1: AWS credentials file (~/.aws/credentials)
[default]
aws_access_key_id = YOUR_ACCESS_KEY
aws_secret_access_key = YOUR_SECRET_KEY

# Option 2: Environment variables
export AWS_ACCESS_KEY_ID=YOUR_ACCESS_KEY
export AWS_SECRET_ACCESS_KEY=YOUR_SECRET_KEY
export AWS_REGION=us-east-1

# Option 3: IAM role (recommended for production)
# No configuration needed - uses instance metadata
```

**Example Queries** (AWS Athena):
```sql
-- Create table
CREATE EXTERNAL TABLE telemetry (
  namespace STRING,
  event_name STRING,
  user_id STRING,
  session_id STRING,
  timestamp BIGINT,
  server_timestamp BIGINT,
  properties MAP<STRING,STRING>,
  source_ip STRING
)
PARTITIONED BY (year INT, month INT, day INT)
STORED AS JSON
LOCATION 's3://game-telemetry-prod/telemetry/my-game/';

-- Add partitions
MSCK REPAIR TABLE telemetry;

-- Query events
SELECT event_name, COUNT(*) as count
FROM telemetry
WHERE year=2026 AND month=2 AND day=16
GROUP BY event_name;
```

---

### 3. PostgreSQL Plugin
**Purpose**: Queryable database for analytics
**Package**: `pkg/storage/plugins/postgres`

Stores telemetry in a PostgreSQL database with JSONB support for flexible properties.

**Features**:
- ✅ Automatic table creation with indexes
- ✅ Bulk INSERT optimization for performance
- ✅ JSONB support for flexible property querying
- ✅ Connection pooling for high throughput
- ✅ Composite indexes for common query patterns

**Database Schema**:
```sql
CREATE TABLE telemetry_events (
    id BIGSERIAL PRIMARY KEY,
    namespace VARCHAR(255) NOT NULL,
    event_name VARCHAR(255) NOT NULL,
    user_id VARCHAR(255),
    session_id VARCHAR(255),
    timestamp BIGINT NOT NULL,
    server_timestamp BIGINT NOT NULL,
    properties JSONB,
    source_ip VARCHAR(45),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes automatically created
CREATE INDEX idx_telemetry_events_namespace_timestamp ON telemetry_events(namespace, timestamp DESC);
CREATE INDEX idx_telemetry_events_user_id ON telemetry_events(user_id);
CREATE INDEX idx_telemetry_events_event_name ON telemetry_events(event_name);
CREATE INDEX idx_telemetry_events_properties ON telemetry_events USING GIN(properties);
```

**Configuration**:
```yaml
- name: postgres
  enabled: true
  batch_size: 200
  flush_interval: 5s
  workers: 8
  retry:
    max_retries: 3
    initial_backoff: 200ms
    max_backoff: 10s
    multiplier: 2.0
  settings:
    dsn: "postgres://user:password@localhost:5432/telemetry?sslmode=disable"
    table: telemetry_events
```

**Example Queries**:
```sql
-- Get events for a user in the last 24 hours
SELECT * FROM telemetry_events
WHERE user_id = 'user123'
  AND timestamp > (EXTRACT(EPOCH FROM NOW() - INTERVAL '24 hours') * 1000)
ORDER BY timestamp DESC;

-- Query by JSONB property
SELECT * FROM telemetry_events
WHERE properties @> '{"level": "5"}'
  AND event_name = 'level_complete';

-- Count events by type
SELECT event_name, COUNT(*) as count
FROM telemetry_events
WHERE namespace = 'my-game'
  AND timestamp > (EXTRACT(EPOCH FROM NOW() - INTERVAL '1 hour') * 1000)
GROUP BY event_name
ORDER BY count DESC;

-- Session replay
SELECT * FROM telemetry_events
WHERE session_id = 'session-456'
ORDER BY timestamp ASC;
```

---

### 4. Kafka Plugin
**Purpose**: Real-time streaming analytics
**Package**: `pkg/storage/plugins/kafka`

Streams telemetry events to Apache Kafka for real-time processing pipelines.

**Features**:
- ✅ Partitioning by user_id for ordered processing
- ✅ Configurable compression (snappy, gzip, lz4, zstd)
- ✅ Batch writes for efficiency
- ✅ Message headers for metadata
- ✅ Health checks for broker connectivity

**Message Format**:
```json
{
  "namespace": "my-game",
  "event_name": "player_death",
  "user_id": "user123",
  "session_id": "session456",
  "timestamp": 1708084800000,
  "server_timestamp": 1708084800123,
  "properties": {
    "level": "5",
    "cause": "enemy"
  },
  "source_ip": "192.168.1.1"
}
```

**Message Headers**:
- `namespace`: Game namespace
- `event_name`: Type of event

**Configuration**:
```yaml
- name: kafka
  enabled: true
  batch_size: 1000
  flush_interval: 1s
  workers: 3
  retry:
    max_retries: 10
    initial_backoff: 100ms
    max_backoff: 5s
    multiplier: 1.5
  settings:
    brokers:
      - localhost:9092
      - kafka-2.example.com:9092
    topic: game-telemetry
    compression: snappy
```

**Consumer Example** (Go):
```go
import "github.com/segmentio/kafka-go"

reader := kafka.NewReader(kafka.ReaderConfig{
    Brokers: []string{"localhost:9092"},
    Topic:   "game-telemetry",
    GroupID: "analytics-consumer",
})

for {
    msg, err := reader.ReadMessage(context.Background())
    if err != nil {
        break
    }

    // Process event
    var event TelemetryEvent
    json.Unmarshal(msg.Value, &event)

    // Access headers
    namespace := string(msg.Headers[0].Value)
    eventName := string(msg.Headers[1].Value)
}
```

---

## Multi-Plugin Configuration

You can enable multiple plugins simultaneously for different use cases:

```yaml
storage:
  plugins:
    # Real-time streaming
    - name: kafka
      enabled: true
      batch_size: 1000
      flush_interval: 1s

    # Long-term archive
    - name: s3
      enabled: true
      batch_size: 500
      flush_interval: 60s

    # Queryable database
    - name: postgres
      enabled: true
      batch_size: 200
      flush_interval: 5s
```

**Use Case Examples**:

1. **Real-time + Archive**
   - Kafka: Stream to analytics pipeline (low latency)
   - S3: Long-term storage (cost-effective)

2. **Hot + Cold Storage**
   - Postgres: Recent data (7 days) for fast queries
   - S3: Historical data (>7 days) for compliance

3. **Redundancy**
   - Multiple S3 buckets in different regions
   - Postgres + S3 for backup

---

## Creating Custom Plugins

Create a new plugin by implementing the `StoragePlugin` interface:

```go
package myplugin

import (
    "context"
    "github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"
)

type MyPlugin struct {
    // Plugin-specific fields
}

func init() {
    // Auto-register on import
    storage.Register("myplugin", func() storage.StoragePlugin {
        return &MyPlugin{}
    })
}

func (p *MyPlugin) Name() string {
    return "myplugin"
}

func (p *MyPlugin) Initialize(ctx context.Context, config storage.PluginConfig) error {
    // Setup plugin (connect to service, validate config, etc.)
    return nil
}

func (p *MyPlugin) Filter(event *storage.TelemetryEvent) bool {
    // Return true to accept event, false to skip
    return true
}

func (p *MyPlugin) transform(event *storage.TelemetryEvent) (interface{}, error) {
    // Convert event to plugin-specific format
    return event, nil
}

func (p *MyPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
    // Write events to storage
    return len(events), nil
}

func (p *MyPlugin) Close() error {
    // Cleanup resources
    return nil
}

func (p *MyPlugin) HealthCheck(ctx context.Context) error {
    // Verify plugin is healthy
    return nil
}
```

Then import in `main.go`:
```go
import (
    _ "github.com/agriardyan/extend-game-telemetry-collector/pkg/storage/plugins/myplugin"
)
```

---

## Performance Tuning

### S3 Plugin
- **Increase batch_size** (500-5000) to reduce API calls
- **Increase flush_interval** (30s-300s) for fewer files
- **More workers** (5-10) for parallel uploads

### PostgreSQL Plugin
- **Tune batch_size** (100-500) based on database capacity
- **Adjust workers** based on connection pool size
- **Use read replicas** for queries
- **Partition table** by timestamp for large datasets

### Kafka Plugin
- **High batch_size** (1000-10000) for throughput
- **Low flush_interval** (100ms-1s) for latency
- **Compression** (snappy for speed, zstd for ratio)
- **Multiple partitions** for parallelism

---

## Monitoring

Each plugin logs important metrics:

```
INFO batch written to s3 key=telemetry/.../1708084800.json count=500 size_bytes=52345
INFO batch written to postgres count=200
INFO batch written to kafka topic=game-telemetry count=1000
```

Monitor these logs for:
- Write latency
- Batch sizes
- Error rates
- Retry attempts
