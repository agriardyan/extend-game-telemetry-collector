# Generic Processor Migration Guide

This document explains the generic implementation of the processor, batcher, and plugin system.

## Overview

The processor has been refactored to use **Go Generics** (Go 1.18+) to support multiple event types across different gRPC services.

### Key Benefits

1. **Type Safety**: Compile-time type checking, no interface{} boxing
2. **Zero Runtime Overhead**: Generics are monomorphized at compile time
3. **Flexibility**: Each service can have its own processor with its own event type
4. **Maintainability**: Single codebase supports all event types

## Architecture

### Generic Components

```go
// Batcher[T] - batches any event type
type Batcher[T any] struct { ... }

// Processor[T] - processes any event type that implements Deduplicatable
type Processor[T storage.Deduplicatable] struct { ... }

// StoragePlugin[T] - stores any event type
type StoragePlugin[T any] interface { ... }

// Deduplicator[T] - deduplicates any event type that implements Deduplicatable
type Deduplicator[T storage.Deduplicatable] interface { ... }
```

### Deduplicatable Interface

Any event type that needs deduplication must implement:

```go
type Deduplicatable interface {
    EventKey() string  // Returns unique identifier for deduplication
}
```

Example:
```go
type TelemetryEvent struct {
    Namespace string
    UserID    string
    // ... other fields
}

func (e *TelemetryEvent) EventKey() string {
    return e.Namespace + ":" + e.UserID + ":" + e.Timestamp
}
```

## Usage Examples

### Example 1: Existing Telemetry Service

```go
// Create plugins typed for TelemetryEvent
plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{
    postgres.NewPostgresPlugin[*storage.TelemetryEvent](),
    s3.NewS3Plugin[*storage.TelemetryEvent](),
}

// Create typed deduplicator
dedup := dedup.NewMemoryDeduplicator[*storage.TelemetryEvent](5 * time.Minute)

// Create typed processor
processor := processor.NewProcessor(
    config,
    plugins,
    dedup,
    logger,
)

processor.Start()

// Submit events
event := &storage.TelemetryEvent{ /* ... */ }
processor.Submit(event)
```

### Example 2: New Chat Service

```go
// Define your event type
type ChatMessage struct {
    RoomID    string
    UserID    string
    Message   string
    Timestamp int64
}

// Implement Deduplicatable
func (c *ChatMessage) EventKey() string {
    return c.RoomID + ":" + c.UserID + ":" + string(rune(c.Timestamp))
}

// Create typed components
plugins := []storage.StoragePlugin[*ChatMessage]{
    mongodb.NewMongoDBPlugin[*ChatMessage](),
}

dedup := dedup.NewMemoryDeduplicator[*ChatMessage](10 * time.Minute)

processor := processor.NewProcessor(
    config,
    plugins,
    dedup,
    logger,
}

processor.Start()
processor.Submit(&ChatMessage{ /* ... */ })
```

### Example 3: Multiple Services in One Application

```go
// Each service gets its own typed processor
telemetryProcessor := processor.NewProcessor[*storage.TelemetryEvent](...)
chatProcessor := processor.NewProcessor[*ChatMessage](...)
metricsProcessor := processor.NewProcessor[*MetricsEvent](...)

// Start all processors
telemetryProcessor.Start()
chatProcessor.Start()
metricsProcessor.Start()

// Each processor is independent with its own:
// - Worker pool
// - Channel buffer
// - Plugins
// - Deduplicator
// - Metrics
```

## Creating Storage Plugins

### Generic Plugin Implementation

```go
type MyPlugin[T any] struct {
    db     *sql.DB
    logger *slog.Logger
}

func NewMyPlugin[T any]() *MyPlugin[T] {
    return &MyPlugin[T]{}
}

func (p *MyPlugin[T]) Name() string {
    return "my-plugin"
}

func (p *MyPlugin[T]) Initialize(ctx context.Context, config storage.PluginConfig) error {
    // Setup connection, etc.
    return nil
}

func (p *MyPlugin[T]) Filter(event T) bool {
    // Decide if this event should be stored
    return true
}

func (p *MyPlugin[T]) WriteBatch(ctx context.Context, events []T) (int, error) {
    // Write events to storage
    // Use type assertions if you need specific fields:
    // if te, ok := any(events[0]).(*storage.TelemetryEvent); ok { ... }
    return len(events), nil
}

func (p *MyPlugin[T]) Close() error {
    return nil
}

func (p *MyPlugin[T]) HealthCheck(ctx context.Context) error {
    return nil
}
```

### Type-Specific Plugin

If a plugin only works with a specific event type:

```go
type PostgresPlugin struct {
    db     *sql.DB
    logger *slog.Logger
}

func (p *PostgresPlugin) Name() string {
    return "postgres"
}

func (p *PostgresPlugin) Filter(event *storage.TelemetryEvent) bool {
    return true
}

func (p *PostgresPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
    // Direct access to TelemetryEvent fields
    for _, event := range events {
        _ = event.Namespace
        _ = event.Data.EventName
    }
    return len(events), nil
}

// ... other methods
```

## Registry System

⚠️ **Important**: The old plugin registry system (`storage.Register()`) is **not compatible** with generics because:
- Generic types need type parameters at instantiation
- A global registry cannot store plugins of different type parameters
- Each service needs its own typed plugin instances

### Migration Strategy

**Old Approach (Registry)**:
```go
// Plugin auto-registers on import
func init() {
    storage.Register("postgres", func() storage.StoragePlugin {
        return &PostgresPlugin{}
    })
}

// Later, load plugins by name
plugin := storage.GetPlugin("postgres")
```

**New Approach (Direct Instantiation)**:
```go
// Import and instantiate directly
import "github.com/.../storage/plugins/postgres"

// For telemetry service
telemetryPlugin := postgres.NewPostgresPlugin[*storage.TelemetryEvent]()

// For chat service
chatPlugin := postgres.NewPostgresPlugin[*ChatMessage]()
```

### Plugin Factory Pattern (Recommended)

```go
// In main.go or service initialization
func createTelemetryPlugins(config Config) []storage.StoragePlugin[*storage.TelemetryEvent] {
    var plugins []storage.StoragePlugin[*storage.TelemetryEvent]
    
    if config.PostgresEnabled {
        plugins = append(plugins, postgres.NewPostgresPlugin[*storage.TelemetryEvent]())
    }
    
    if config.S3Enabled {
        plugins = append(plugins, s3.NewS3Plugin[*storage.TelemetryEvent]())
    }
    
    if config.KafkaEnabled {
        plugins = append(plugins, kafka.NewKafkaPlugin[*storage.TelemetryEvent]())
    }
    
    return plugins
}
```

## Deduplicators

### Memory Deduplicator
```go
// Good for single-instance deployments
dedup := dedup.NewMemoryDeduplicator[*YourEventType](5 * time.Minute)
```

### Redis Deduplicator
```go
// Good for multi-instance deployments (shared state)
dedup := dedup.NewRedisDeduplicator[*YourEventType](
    dedup.RedisConfig{
        Addr: "localhost:6379",
        Password: "",
        DB: 0,
    },
    5 * time.Minute,
)
```

### No Deduplicator
```go
// When deduplication is not needed
dedup := dedup.NewNoopDeduplicator[*YourEventType]()
```

## Performance Considerations

1. **Separate Processors**: Each event type gets its own processor instance with independent resources
2. **Resource Allocation**: Tune `Workers`, `ChannelBuffer`, `BatchSize`, and `FlushInterval` per service
3. **Plugin Isolation**: Slow plugins in one service don't affect other services
4. **Zero Abstraction Cost**: Generics are compiled away, no runtime overhead

## Testing

See `pkg/processor/examples_test.go` for comprehensive examples of:
- Single event type usage
- Multiple event types
- Custom plugins
- With/without deduplication

## Migration Checklist

- [x] Batcher made generic
- [x] Processor made generic
- [x] StoragePlugin interface made generic
- [x] Deduplicator made generic
- [x] Deduplicatable interface created
- [x] NoopPlugin updated
- [x] MemoryDeduplicator updated
- [x] RedisDeduplicator updated
- [ ] Update existing storage plugins (postgres, s3, kafka, mongodb)
- [ ] Update main.go to use new instantiation pattern
- [ ] Update tests
- [ ] Remove deprecated registry system (optional)

## Next Steps for Other Plugins

Each storage plugin needs to be updated to be generic. Two approaches:

### Approach 1: Generic Plugin (works with any type)
```go
type S3Plugin[T any] struct { ... }
func NewS3Plugin[T any]() *S3Plugin[T] { ... }
```

### Approach 2: Type-Specific Plugin (works with specific type only)
```go
type S3Plugin struct { ... }
func NewS3Plugin() storage.StoragePlugin[*storage.TelemetryEvent] { ... }
```

Choose based on whether the plugin needs to know about specific event fields or can work generically.
