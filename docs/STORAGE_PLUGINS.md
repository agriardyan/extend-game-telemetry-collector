# Storage Plugins Guide

This guide covers all available storage plugins for the Game Telemetry Collector and how to use them effectively.

## Overview

The telemetry collector supports multiple storage backends simultaneously through a pluggable architecture. Each plugin can be independently configured with its own batching, retry, and filtering logic.

**Available Plugins:**
- [S3](#s3-plugin) - Cost-effective data lake storage
- [PostgreSQL](#postgresql-plugin) - Relational database with JSONB support
- [Kafka](#kafka-plugin) - Real-time event streaming
- [MongoDB](#mongodb-plugin) - Flexible document storage
- [No-op](#noop-plugin) - Testing and development

---

## S3 Plugin

### Overview
Amazon S3 plugin stores telemetry events as JSON files with automatic date-based partitioning. Perfect for long-term storage and analytics with tools like Amazon Athena, AWS Glue, or Apache Spark.

### Features
- ✅ Automatic partitioning by `namespace/year/month/day`
- ✅ Cost-effective storage ($0.023/GB/month)
- ✅ Compatible with AWS analytics services
- ✅ Configurable batch sizes and compression

### Configuration

```yaml
storage:
  plugins:
    - name: s3
      enabled: true
      batch_size: 500           # Events per file
      flush_interval: 10s       # Max time before creating new file
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

### File Structure

```
s3://game-telemetry-prod/
└── telemetry/
    └── my-game/
        ├── year=2026/
        │   └── month=02/
        │       └── day=16/
        │           ├── 1708084800.json
        │           ├── 1708084810.json
        │           └── 1708084820.json
```

### Example File Content

```json
[
  {
    "namespace": "my-game",
    "event_name": "player_death",
    "user_id": "user123",
    "session_id": "session456",
    "timestamp": 1708084800000,
    "server_timestamp": 1708084800123,
    "source_ip": "203.0.113.42",
    "properties": {
      "level": "5",
      "cause": "enemy",
      "weapon": "sword"
    }
  }
]
```

### Querying with Amazon Athena

```sql
CREATE EXTERNAL TABLE telemetry_events (
  namespace STRING,
  event_name STRING,
  user_id STRING,
  timestamp BIGINT,
  properties MAP<STRING, STRING>
)
PARTITIONED BY (year INT, month INT, day INT)
STORED AS JSON
LOCATION 's3://game-telemetry-prod/telemetry/my-game/';

-- Add partitions
MSCK REPAIR TABLE telemetry_events;

-- Query
SELECT event_name, COUNT(*) as count
FROM telemetry_events
WHERE year=2026 AND month=2 AND day=16
GROUP BY event_name;
```

### AWS Credentials

The plugin uses AWS SDK v2 credential chain:
1. Environment variables (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`)
2. IAM role (if running on EC2/ECS)
3. AWS credentials file (`~/.aws/credentials`)

---

## PostgreSQL Plugin

### Overview
PostgreSQL plugin stores telemetry in a relational database with JSONB support for flexible properties. Ideal for fast queries and SQL-based analytics.

### Features
- ✅ JSONB column for flexible property storage
- ✅ Automatic table and index creation
- ✅ Bulk INSERT optimization
- ✅ Sub-second query performance

### Configuration

```yaml
storage:
  plugins:
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

### Schema

The plugin automatically creates this schema:

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

-- Indexes
CREATE INDEX idx_namespace_timestamp ON telemetry_events(namespace, timestamp);
CREATE INDEX idx_user_id ON telemetry_events(user_id);
CREATE INDEX idx_event_name ON telemetry_events(event_name);
CREATE INDEX idx_session_id ON telemetry_events(session_id);
CREATE INDEX idx_timestamp ON telemetry_events(timestamp);
CREATE INDEX idx_properties ON telemetry_events USING GIN(properties);
```

### Example Queries

```sql
-- Query by JSONB property
SELECT * FROM telemetry_events
WHERE properties @> '{"level": "5"}';

-- Time-series analysis
SELECT
    event_name,
    COUNT(*) as count,
    DATE_TRUNC('hour', TO_TIMESTAMP(timestamp/1000)) as hour
FROM telemetry_events
WHERE timestamp > EXTRACT(EPOCH FROM NOW() - INTERVAL '24 hours') * 1000
GROUP BY event_name, hour
ORDER BY hour DESC;

-- User session analysis
SELECT
    session_id,
    COUNT(*) as events,
    MIN(timestamp) as session_start,
    MAX(timestamp) as session_end
FROM telemetry_events
WHERE user_id = 'user123'
GROUP BY session_id;
```

---

## Kafka Plugin

### Overview
Kafka plugin streams telemetry events in real-time to Apache Kafka. Perfect for building real-time analytics, monitoring, and event-driven architectures.

### Features
- ✅ Real-time streaming (<100ms latency)
- ✅ Partitioning by user_id for ordered processing
- ✅ Configurable compression (snappy, gzip, lz4, zstd)
- ✅ High throughput (50K+ events/second)

### Configuration

```yaml
storage:
  plugins:
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
          - kafka-1.example.com:9092
          - kafka-2.example.com:9092
          - kafka-3.example.com:9092
        topic: game-telemetry
        compression: snappy
```

### Message Format

**Key**: `user_id` (for partitioning)
**Value**: JSON-encoded event

```json
{
  "namespace": "my-game",
  "event_name": "player_death",
  "user_id": "user123",
  "session_id": "session456",
  "timestamp": 1708084800000,
  "server_timestamp": 1708084800123,
  "properties": {
    "level": "5"
  }
}
```

**Headers**:
- `event_name`: Event type for filtering
- `namespace`: Game namespace

### Consumer Example

```go
reader := kafka.NewReader(kafka.ReaderConfig{
    Brokers: []string{"localhost:9092"},
    Topic:   "game-telemetry",
    GroupID: "analytics",
})

for {
    msg, _ := reader.ReadMessage(ctx)

    var event TelemetryEvent
    json.Unmarshal(msg.Value, &event)

    // Process event...
    fmt.Printf("Event: %s from %s\n", event.EventName, event.UserId)
}
```

---

## MongoDB Plugin

### Overview
MongoDB plugin stores telemetry in MongoDB or AWS DocumentDB. Perfect for flexible schema requirements and rich queries on nested properties.

### Features
- ✅ Flexible schema (no migrations needed)
- ✅ Automatic index creation
- ✅ Rich query capabilities
- ✅ AWS DocumentDB compatible

### Configuration

```yaml
storage:
  plugins:
    - name: mongodb
      enabled: true
      batch_size: 500
      flush_interval: 5s
      workers: 5
      retry:
        max_retries: 3
        initial_backoff: 200ms
        max_backoff: 10s
        multiplier: 2.0
      settings:
        uri: "mongodb://localhost:27017"
        database: telemetry
        collection: events
```

### Document Structure

```json
{
  "_id": ObjectId("..."),
  "namespace": "my-game",
  "event_name": "player_death",
  "user_id": "user123",
  "session_id": "session456",
  "timestamp": 1708084800000,
  "server_timestamp": 1708084800123,
  "source_ip": "203.0.113.42",
  "properties": {
    "level": "5",
    "cause": "enemy",
    "weapon": "sword"
  },
  "created_at": ISODate("2026-02-16T12:00:00Z")
}
```

### Indexes

The plugin automatically creates these indexes:
- `namespace + timestamp` (compound)
- `user_id` (single)
- `event_name` (single)
- `session_id + timestamp` (compound)
- `server_timestamp` (single)

### Example Queries

```javascript
// Find all deaths in level 5
db.events.find({
  event_name: "player_death",
  "properties.level": "5"
})

// Time-series aggregation
db.events.aggregate([
  {
    $match: {
      timestamp: { $gte: Date.now() - 3600000 }
    }
  },
  {
    $group: {
      _id: "$event_name",
      count: { $sum: 1 }
    }
  }
])

// User session timeline
db.events.find({
  user_id: "user123",
  session_id: "session456"
}).sort({ timestamp: 1 })
```

### AWS DocumentDB

To use AWS DocumentDB:

```yaml
settings:
  uri: "mongodb://username:password@docdb-cluster.cluster-xxxxx.us-east-1.docdb.amazonaws.com:27017/?tls=true&tlsCAFile=rds-combined-ca-bundle.pem&replicaSet=rs0&readPreference=secondaryPreferred&retryWrites=false"
  database: telemetry
  collection: events
```

---

## No-op Plugin

### Overview
No-op plugin discards all events. Useful for testing and benchmarking without storage overhead.

### Configuration

```yaml
storage:
  plugins:
    - name: noop
      enabled: true
      batch_size: 100
      flush_interval: 5s
      workers: 2
      settings: {}
```

---

## Multi-Plugin Strategies

### Strategy 1: Hot + Cold Storage

```yaml
storage:
  plugins:
    # Hot storage for recent events (7 days)
    - name: postgres
      enabled: true
      batch_size: 200
      flush_interval: 5s

    # Cold storage for historical data
    - name: s3
      enabled: true
      batch_size: 1000
      flush_interval: 60s
```

### Strategy 2: Real-time + Archive

```yaml
storage:
  plugins:
    # Real-time streaming for live analytics
    - name: kafka
      enabled: true
      batch_size: 500
      flush_interval: 1s

    # Archive for compliance and auditing
    - name: s3
      enabled: true
      batch_size: 2000
      flush_interval: 300s
```

### Strategy 3: Flexible Schema + Fast Queries

```yaml
storage:
  plugins:
    # MongoDB for flexible schema
    - name: mongodb
      enabled: true
      batch_size: 500
      flush_interval: 5s

    # PostgreSQL for structured queries
    - name: postgres
      enabled: true
      batch_size: 200
      flush_interval: 5s
```

---

## Performance Tuning

### Batch Size

- **Small batches (50-100)**: Lower latency, more write operations
- **Large batches (500-1000)**: Higher throughput, better efficiency
- **S3**: 500-2000 (files are cheap)
- **PostgreSQL**: 100-500 (balance between latency and overhead)
- **Kafka**: 1000-5000 (designed for high throughput)
- **MongoDB**: 500-1000 (good balance)

### Flush Interval

- **Real-time** (1-2s): Kafka, monitoring dashboards
- **Near real-time** (5-10s): PostgreSQL, MongoDB
- **Batch** (30-300s): S3, data warehousing

### Workers

- **I/O bound** (S3, MongoDB): 5-10 workers
- **Connection pooled** (PostgreSQL, Kafka): Match connection pool size
- **CPU bound**: Number of CPU cores

---

## Creating Custom Plugins

See [PLUGIN_DEVELOPMENT.md](./PLUGIN_DEVELOPMENT.md) for a detailed guide on creating custom storage plugins.

**Quick Example:**

```go
package custom

import (
    "context"
    "github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"
)

type CustomPlugin struct {
    // Your fields
}

func init() {
    storage.Register("custom", func() storage.StoragePlugin {
        return &CustomPlugin{}
    })
}

func (p *CustomPlugin) Name() string { return "custom" }

func (p *CustomPlugin) Initialize(ctx context.Context, config storage.PluginConfig) error {
    // Setup
    return nil
}

func (p *CustomPlugin) Filter(event *storage.TelemetryEvent) bool {
    // Filter logic
    return true
}

func (p *CustomPlugin) transform(event *storage.TelemetryEvent) (interface{}, error) {
    // Transform logic
    return event, nil
}

func (p *CustomPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
    // Write logic
    return len(events), nil
}

func (p *CustomPlugin) Close() error { return nil }
func (p *CustomPlugin) HealthCheck(ctx context.Context) error { return nil }
```

Then import in `main.go`:
```go
import _ "your-module/pkg/storage/plugins/custom"
```
