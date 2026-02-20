// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package mongodb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDBPluginConfig holds MongoDB/DocumentDB-specific configuration.
type MongoDBPluginConfig struct {
	URI        string // required, e.g. "mongodb://localhost:27017"
	Database   string // default "telemetry"
	Collection string // default "events"
	Workers    int    // controls connection pool size; default 2
}

// MongoDBPlugin stores telemetry events in MongoDB/DocumentDB.
type MongoDBPlugin struct {
	client     *mongo.Client
	database   *mongo.Database
	collection *mongo.Collection
	cfg        MongoDBPluginConfig
}

// NewMongoDBPlugin creates a new MongoDB storage plugin with the given configuration.
func NewMongoDBPlugin(cfg MongoDBPluginConfig) storage.StoragePlugin[*storage.TelemetryEvent] {
	if cfg.Database == "" {
		cfg.Database = "telemetry"
	}
	if cfg.Collection == "" {
		cfg.Collection = "events"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	return &MongoDBPlugin{cfg: cfg}
}

func (p *MongoDBPlugin) Name() string {
	return "mongodb"
}

func (p *MongoDBPlugin) Initialize(ctx context.Context) error {
	if p.cfg.URI == "" {
		return fmt.Errorf("mongodb URI is required")
	}

	// Connect to MongoDB
	clientOptions := options.Client().
		ApplyURI(p.cfg.URI).
		SetMaxPoolSize(uint64(p.cfg.Workers * 2)).
		SetMinPoolSize(uint64(p.cfg.Workers)).
		SetServerSelectionTimeout(10 * time.Second)

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return fmt.Errorf("failed to connect to mongodb: %w", err)
	}

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("failed to ping mongodb: %w", err)
	}

	p.client = client
	p.database = client.Database(p.cfg.Database)
	p.collection = p.database.Collection(p.cfg.Collection)

	// Create indexes for efficient queries
	if err := p.createIndexes(ctx); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return nil
}

func (p *MongoDBPlugin) createIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		// Compound index for namespace + server_timestamp queries
		{
			Keys: bson.D{
				{Key: "namespace", Value: 1},
				{Key: "server_timestamp", Value: -1},
			},
		},
		// Index for user_id queries
		{
			Keys: bson.D{{Key: "user_id", Value: 1}},
		},
		// Index for kind queries
		{
			Keys: bson.D{{Key: "kind", Value: 1}},
		},
		// Compound index for kind + namespace queries
		{
			Keys: bson.D{
				{Key: "kind", Value: 1},
				{Key: "namespace", Value: 1},
			},
		},
		// Index for event_id queries
		{
			Keys: bson.D{{Key: "event_id", Value: 1}},
		},
	}

	// Create all indexes
	opts := options.CreateIndexes().SetMaxTime(30 * time.Second)
	_, err := p.collection.Indexes().CreateMany(ctx, indexes, opts)
	return err
}

// Filter determines if an event should be processed by this plugin
// ------------------------------------------------------------------------------
// DEVELOPER NOTE:
// This method is where you can implement custom filtering logic to control which events are processed by this plugin.
// For example, you might want to filter out certain event types, namespaces, or events from specific users.
// You can modify this logic as needed for your specific use case.
// ------------------------------------------------------------------------------
func (p *MongoDBPlugin) Filter(event *storage.TelemetryEvent) bool {
	// Default: accept all events
	// Developers can customize this by modifying the plugin code
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
func (p *MongoDBPlugin) transform(event *storage.TelemetryEvent) (any, error) {
	flat := event.ToDocument()

	// JSON round-trip for the payload so BSON uses json tag field names
	if payload, ok := flat["payload"]; ok {
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		var payloadMap map[string]interface{}
		if err := json.Unmarshal(payloadBytes, &payloadMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
		}
		flat["payload"] = payloadMap
	}

	doc := bson.M{"created_at": time.Now()}
	for k, v := range flat {
		doc[k] = v
	}
	return doc, nil
}

func (p *MongoDBPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	// Transform all events
	documents := make([]any, 0, len(events))
	for _, event := range events {
		doc, err := p.transform(event)
		if err != nil {
			continue // Skip invalid events
		}
		documents = append(documents, doc)
	}

	if len(documents) == 0 {
		return 0, nil
	}

	// Batch insert with ordered=false for partial success
	opts := options.InsertMany().SetOrdered(false)
	result, err := p.collection.InsertMany(ctx, documents, opts)
	if err != nil {
		// Even with errors, some documents may have been inserted
		if result != nil {
			return len(result.InsertedIDs), err
		}
		return 0, err
	}

	return len(result.InsertedIDs), nil
}

func (p *MongoDBPlugin) Close() error {
	if p.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return p.client.Disconnect(ctx)
	}
	return nil
}

func (p *MongoDBPlugin) HealthCheck(ctx context.Context) error {
	if p.client == nil {
		return fmt.Errorf("mongodb client not initialized")
	}

	// Ping the database
	return p.client.Ping(ctx, nil)
}
