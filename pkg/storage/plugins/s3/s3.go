// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3PluginConfig holds S3-specific configuration.
type S3PluginConfig struct {
	Bucket string // required
	Prefix string // default "telemetry"
	Region string // default "us-east-1"
}

// S3Plugin stores telemetry events as JSON files in Amazon S3.
// Files are partitioned by namespace and date for efficient querying.
type S3Plugin struct {
	client *s3.Client
	cfg    S3PluginConfig
	logger *slog.Logger
}

// NewS3Plugin creates a new S3 storage plugin with the given configuration.
func NewS3Plugin(cfg S3PluginConfig) storage.StoragePlugin[*storage.TelemetryEvent] {
	if cfg.Prefix == "" {
		cfg.Prefix = "telemetry"
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	return &S3Plugin{cfg: cfg}
}

func (p *S3Plugin) Name() string {
	return "s3"
}

func (p *S3Plugin) Initialize(ctx context.Context) error {
	p.logger = slog.Default().With("plugin", "s3")

	if p.cfg.Bucket == "" {
		return fmt.Errorf("s3 bucket is required")
	}

	// Load AWS SDK configuration
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(p.cfg.Region),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	p.client = s3.NewFromConfig(cfg)

	p.logger.Info("s3 plugin initialized",
		"bucket", p.cfg.Bucket,
		"prefix", p.cfg.Prefix,
		"region", p.cfg.Region)

	return nil
}

func (p *S3Plugin) Filter(event *storage.TelemetryEvent) bool {
	// Default: accept all events
	// Developers can override this method to implement custom filtering
	return true
}

func (p *S3Plugin) transform(event *storage.TelemetryEvent) (interface{}, error) {
	return event.ToDocument(), nil
}

func (p *S3Plugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	// Transform all events
	var transformedEvents []interface{}
	for _, event := range events {
		transformed, err := p.transform(event)
		if err != nil {
			p.logger.Warn("failed to transform event, skipping",
				"error", err,
				"kind", event.Kind,
				"user_id", event.UserID)
			continue
		}
		transformedEvents = append(transformedEvents, transformed)
	}

	if len(transformedEvents) == 0 {
		return 0, fmt.Errorf("all events failed transformation")
	}

	// Serialize to JSON
	data, err := json.MarshalIndent(transformedEvents, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("failed to marshal events: %w", err)
	}

	// Generate S3 key with partitioning
	// Format: {prefix}/{namespace}/year=YYYY/month=MM/day=DD/{timestamp}.json
	namespace := events[0].Namespace
	now := time.Now().UTC()
	key := fmt.Sprintf("%s/%s/year=%d/month=%02d/day=%02d/%d.json",
		p.cfg.Prefix,
		namespace,
		now.Year(),
		now.Month(),
		now.Day(),
		now.UnixMilli())

	// Upload to S3
	_, err = p.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.cfg.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
		Metadata: map[string]string{
			"namespace":   namespace,
			"event_count": fmt.Sprintf("%d", len(transformedEvents)),
			"created_at":  now.Format(time.RFC3339),
		},
	})

	if err != nil {
		return 0, fmt.Errorf("failed to upload to S3: %w", err)
	}

	p.logger.Info("batch written to S3",
		"key", key,
		"count", len(transformedEvents),
		"size_bytes", len(data))

	return len(transformedEvents), nil
}

func (p *S3Plugin) Close() error {
	p.logger.Info("s3 plugin closed")
	// S3 client doesn't require explicit cleanup
	return nil
}

func (p *S3Plugin) HealthCheck(ctx context.Context) error {
	// Check if bucket exists and is accessible
	_, err := p.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(p.cfg.Bucket),
	})

	if err != nil {
		return fmt.Errorf("S3 health check failed: %w", err)
	}

	return nil
}
