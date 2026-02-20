// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"

	"github.com/segmentio/kafka-go"
)

// KafkaPlugin streams telemetry events to Apache Kafka
// Provides real-time event streaming for analytics pipelines
type KafkaPlugin struct {
	writer *kafka.Writer
	logger *slog.Logger

	cfg KafkaPluginConfig
}

type KafkaPluginConfig struct {
	Brokers       []string
	Topic         string
	Compression   string
	BatchSize     int
	FlushInterval time.Duration
}

// NewKafkaPlugin creates a new Kafka storage plugin
func NewKafkaPlugin(cfg KafkaPluginConfig) storage.StoragePlugin[*storage.TelemetryEvent] {
	return &KafkaPlugin{
		cfg: cfg,
	}
}

func (p *KafkaPlugin) Name() string {
	return "kafka"
}

func (p *KafkaPlugin) Initialize(ctx context.Context) error {
	p.logger = slog.Default().With("plugin", "kafka")

	if p.cfg.Compression == "" {
		p.cfg.Compression = "snappy"
	}

	// Create Kafka writer
	p.writer = &kafka.Writer{
		Addr:         kafka.TCP(p.cfg.Brokers...),
		Topic:        p.cfg.Topic,
		Balancer:     &kafka.Hash{}, // Partition by message key (user_id)
		BatchSize:    p.cfg.BatchSize,
		BatchTimeout: p.cfg.FlushInterval,
		Compression:  p.getCompression(p.cfg.Compression),
		Async:        false, // Synchronous for reliability (can be made async for performance)
		RequiredAcks: kafka.RequireAll,
	}

	p.logger.Info("kafka plugin initialized",
		"topic", p.cfg.Topic,
		"brokers", p.cfg.Brokers,
		"compression", p.cfg.Compression,
		"batch_size", p.cfg.BatchSize)

	return nil
}

func (p *KafkaPlugin) getCompression(compression string) kafka.Compression {
	switch compression {
	case "gzip":
		return kafka.Gzip
	case "snappy":
		return kafka.Snappy
	case "lz4":
		return kafka.Lz4
	case "zstd":
		return kafka.Zstd
	default:
		return kafka.Snappy
	}
}

// Filter determines if an event should be processed by this plugin
// ------------------------------------------------------------------------------
// DEVELOPER NOTE:
// This method is where you can implement custom filtering logic to control which events are processed by this plugin.
// For example, you might want to filter out certain event types, namespaces, or events from specific users.
// You can modify this logic as needed for your specific use case.
// ------------------------------------------------------------------------------
func (p *KafkaPlugin) Filter(event *storage.TelemetryEvent) bool {
	// Default: accept all events
	// Developers can override this method to implement custom filtering
	return true
}

// transform converts a TelemetryEvent into a format suitable for this plugin's insertion
// ------------------------------------------------------------------------------
// DEVELOPER NOTE:
// This method is where you can customize how the event data is transformed before being stored.
// For example, you might want to flatten nested properties, convert timestamps, or filter out certain fields.
// The current implementation converts the properties map to a JSON string for storage in a JSONB column.
// You can modify this logic as needed for your specific use case.
// ------------------------------------------------------------------------------
func (p *KafkaPlugin) transform(event *storage.TelemetryEvent) (interface{}, error) {
	data, err := json.Marshal(event.ToDocument())
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event: %w", err)
	}
	return data, nil
}

func (p *KafkaPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	// Transform events to Kafka messages
	messages := make([]kafka.Message, 0, len(events))

	for _, event := range events {
		data, err := p.transform(event)
		if err != nil {
			p.logger.Warn("failed to transform event, skipping",
				"error", err,
				"kind", event.Kind,
				"user_id", event.UserID)
			continue
		}

		messages = append(messages, kafka.Message{
			Key:   []byte(event.UserID), // Partition by user_id for ordering
			Value: data.([]byte),
			Time:  time.UnixMilli(event.ServerTimestamp),
			Headers: []kafka.Header{
				{Key: "namespace", Value: []byte(event.Namespace)},
				{Key: "kind", Value: []byte(event.Kind)},
			},
		})
	}

	if len(messages) == 0 {
		return 0, fmt.Errorf("all events failed transformation")
	}

	// Write messages to Kafka
	err := p.writer.WriteMessages(ctx, messages...)
	if err != nil {
		return 0, fmt.Errorf("failed to write messages to Kafka: %w", err)
	}

	p.logger.Info("batch written to kafka",
		"topic", p.cfg.Topic,
		"count", len(messages))

	return len(messages), nil
}

func (p *KafkaPlugin) Close() error {
	p.logger.Info("kafka plugin closing")
	if p.writer != nil {
		return p.writer.Close()
	}
	return nil
}

func (p *KafkaPlugin) HealthCheck(ctx context.Context) error {
	// Check if we can connect to Kafka brokers
	conn, err := kafka.DialContext(ctx, "tcp", p.cfg.Brokers[0])
	if err != nil {
		return fmt.Errorf("Kafka health check failed: %w", err)
	}
	defer conn.Close()

	// Verify topic exists
	partitions, err := conn.ReadPartitions(p.cfg.Topic)
	if err != nil {
		return fmt.Errorf("failed to read topic partitions: %w", err)
	}

	if len(partitions) == 0 {
		return fmt.Errorf("topic %s has no partitions", p.cfg.Topic)
	}

	return nil
}
