// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"

	_ "github.com/lib/pq"
)

// PostgresPluginConfig holds PostgreSQL-specific configuration.
type PostgresPluginConfig struct {
	DSN     string // required, e.g. "postgres://user:pass@host/db?sslmode=disable"
	Table   string // default "telemetry_events"
	Workers int    // controls connection pool size; default 2
}

// PostgresPlugin stores telemetry events in a PostgreSQL database.
// Uses JSONB for flexible payload storage and efficient querying.
type PostgresPlugin struct {
	db     *sql.DB
	cfg    PostgresPluginConfig
	logger *slog.Logger
}

// NewPostgresPlugin creates a new PostgreSQL storage plugin with the given configuration.
func NewPostgresPlugin(cfg PostgresPluginConfig) storage.StoragePlugin[*storage.TelemetryEvent] {
	if cfg.Table == "" {
		cfg.Table = "telemetry_events"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	return &PostgresPlugin{cfg: cfg}
}

func (p *PostgresPlugin) Name() string {
	return "postgres"
}

func (p *PostgresPlugin) Initialize(ctx context.Context) error {
	p.logger = slog.Default().With("plugin", "postgres")

	if p.cfg.DSN == "" {
		return fmt.Errorf("postgres DSN is required")
	}

	// Open database connection
	var err error
	p.db, err = sql.Open("postgres", p.cfg.DSN)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	maxOpenConns := p.cfg.Workers * 2
	if maxOpenConns < 10 {
		maxOpenConns = 10
	}
	p.db.SetMaxOpenConns(maxOpenConns)
	p.db.SetMaxIdleConns(maxOpenConns / 2)

	// Test connection
	if err := p.db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// Create table if not exists.
	// In production, consider using migrations instead of auto-creating tables.
	if err := p.createTableIfNotExists(ctx); err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	p.logger.Info("postgres plugin initialized",
		"table", p.cfg.Table,
		"max_open_conns", maxOpenConns)

	return nil
}

// createTableIfNotExists creates the telemetry_events table if it doesn't exist
// In production, consider using migrations instead of auto-creating tables
func (p *PostgresPlugin) createTableIfNotExists(ctx context.Context) error {
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			kind VARCHAR(50) NOT NULL,
			namespace VARCHAR(255) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			event_id VARCHAR(255) NOT NULL,
			timestamp VARCHAR(255),
			server_timestamp BIGINT NOT NULL,
			payload JSONB NOT NULL,
			source_ip VARCHAR(45),
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_%s_namespace_server_timestamp
			ON %s(namespace, server_timestamp DESC);

		CREATE INDEX IF NOT EXISTS idx_%s_user_id
			ON %s(user_id);

		CREATE INDEX IF NOT EXISTS idx_%s_kind
			ON %s(kind);

		CREATE INDEX IF NOT EXISTS idx_%s_payload
			ON %s USING GIN(payload);
	`, p.cfg.Table,
		p.cfg.Table, p.cfg.Table,
		p.cfg.Table, p.cfg.Table,
		p.cfg.Table, p.cfg.Table,
		p.cfg.Table, p.cfg.Table)

	_, err := p.db.ExecContext(ctx, query)
	return err
}

// Filter determines if an event should be processed by this plugin
// ------------------------------------------------------------------------------
// DEVELOPER NOTE:
// This method is where you can implement custom filtering logic to control which events are processed by this plugin.
// For example, you might want to filter out certain event types, namespaces, or events from specific users.
// You can modify this logic as needed for your specific use case.
// ------------------------------------------------------------------------------
func (p *PostgresPlugin) Filter(event *storage.TelemetryEvent) bool {
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
func (p *PostgresPlugin) transform(event *storage.TelemetryEvent) (interface{}, error) {
	doc := event.ToDocument()

	// Marshal the typed payload to JSON for the JSONB column
	payloadJSON, err := json.Marshal(doc["payload"])
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	return map[string]interface{}{
		"kind":             doc["kind"],
		"namespace":        doc["namespace"],
		"user_id":          doc["user_id"],
		"event_id":         doc["event_id"],
		"timestamp":        doc["timestamp"],
		"server_timestamp": doc["server_timestamp"],
		"payload":          string(payloadJSON),
		"source_ip":        doc["source_ip"],
	}, nil
}

func (p *PostgresPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	// Use a transaction for batch insert
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Build bulk INSERT statement
	// Using parameterized query to prevent SQL injection
	valueStrings := make([]string, 0, len(events))
	valueArgs := make([]interface{}, 0, len(events)*8)
	argPosition := 1

	for _, event := range events {
		transformed, err := p.transform(event)
		if err != nil {
			p.logger.Warn("failed to transform event, skipping",
				"error", err,
				"kind", event.Kind,
				"user_id", event.UserID)
			continue
		}

		data := transformed.(map[string]interface{})

		valueStrings = append(valueStrings,
			fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
				argPosition, argPosition+1, argPosition+2, argPosition+3,
				argPosition+4, argPosition+5, argPosition+6, argPosition+7))

		valueArgs = append(valueArgs,
			data["kind"],
			data["namespace"],
			data["user_id"],
			data["event_id"],
			data["timestamp"],
			data["server_timestamp"],
			data["payload"],
			data["source_ip"],
		)

		argPosition += 8
	}

	if len(valueStrings) == 0 {
		return 0, fmt.Errorf("all events failed transformation")
	}

	// Execute bulk INSERT
	query := fmt.Sprintf(`
		INSERT INTO %s
		(kind, namespace, user_id, event_id, timestamp, server_timestamp, payload, source_ip)
		VALUES %s
	`, p.cfg.Table, strings.Join(valueStrings, ","))

	result, err := tx.ExecContext(ctx, query, valueArgs...)
	if err != nil {
		return 0, fmt.Errorf("failed to insert events: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()

	p.logger.Info("batch written to postgres",
		"count", rowsAffected)

	return int(rowsAffected), nil
}

func (p *PostgresPlugin) Close() error {
	p.logger.Info("postgres plugin closing")
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

func (p *PostgresPlugin) HealthCheck(ctx context.Context) error {
	return p.db.PingContext(ctx)
}
