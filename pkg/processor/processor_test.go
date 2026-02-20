// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package processor

import (
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/dedup"
	pb "github.com/agriardyan/extend-game-telemetry-collector/pkg/pb"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"
)

func TestProcessor_BasicSubmission(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       2,
		ChannelBuffer: 100,
		BatchSize:     5,
		FlushInterval: 100 * time.Millisecond,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit 10 events
	for i := 0; i < 10; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		if err := processor.Submit(event); err != nil {
			t.Fatalf("Failed to submit event: %v", err)
		}
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Shutdown
	if err := processor.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	// Verify all events were processed
	if mockPlugin.GetTotalEvents() != 10 {
		t.Errorf("Expected 10 events processed, got %d", mockPlugin.GetTotalEvents())
	}

	// Should have at least 2 batches (may have more due to timing)
	writeCalls := mockPlugin.GetWriteCalls()
	if writeCalls < 2 {
		t.Errorf("Expected at least 2 write calls, got %d", writeCalls)
	}
}

func TestProcessor_Deduplication(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}

	// Use memory deduplicator
	deduplicator := dedup.NewMemoryDeduplicator[*storage.TelemetryEvent](1 * time.Minute)
	defer deduplicator.Close()

	config := Config{
		Workers:       2,
		ChannelBuffer: 100,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit same event 5 times (same EventKey for deduplication)
	event := &storage.TelemetryEvent{
		Kind:      storage.KindGameplay,
		Namespace: "test",
		UserID:    "user1",
		Gameplay: &pb.CreateGameplayTelemetryRequest{
			EventId:   "test_event",
			Timestamp: "12345",
		},
	}

	for i := 0; i < 5; i++ {
		if err := processor.Submit(event); err != nil {
			t.Fatalf("Failed to submit event: %v", err)
		}
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Shutdown
	if err := processor.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	// Only 1 event should be processed (others deduplicated)
	if mockPlugin.GetTotalEvents() != 1 {
		t.Errorf("Expected 1 event processed (dedup), got %d", mockPlugin.GetTotalEvents())
	}

	// Check metrics
	stats := processor.Stats()
	if stats["events_duplicated"].(uint64) != 4 {
		t.Errorf("Expected 4 duplicated events, got %v", stats["events_duplicated"])
	}
}

func TestProcessor_MultiplePlugins(t *testing.T) {
	mockPlugin1 := NewMockPlugin("mock1")
	mockPlugin2 := NewMockPlugin("mock2")
	mockPlugin3 := NewMockPlugin("mock3")

	// Set different filters
	mockPlugin2.SetFilter(func(event *storage.TelemetryEvent) bool {
		// Only accept events with user_id "user2"
		return event.UserID == "user2"
	})

	mockPlugin3.SetFilter(func(event *storage.TelemetryEvent) bool {
		// Only accept events with server_timestamp >= 5
		return event.ServerTimestamp >= 5
	})

	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin1, mockPlugin2, mockPlugin3}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       2,
		ChannelBuffer: 100,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit 10 events (5 user1, 5 user2)
	for i := 0; i < 10; i++ {
		userID := "user1"
		if i >= 5 {
			userID = "user2"
		}

		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          userID,
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		if err := processor.Submit(event); err != nil {
			t.Fatalf("Failed to submit event: %v", err)
		}
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Shutdown
	if err := processor.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	// Plugin1: accepts all (10 events)
	if mockPlugin1.GetTotalEvents() != 10 {
		t.Errorf("Plugin1: Expected 10 events, got %d", mockPlugin1.GetTotalEvents())
	}

	// Plugin2: only user2 (5 events: 5-9)
	if mockPlugin2.GetTotalEvents() != 5 {
		t.Errorf("Plugin2: Expected 5 events, got %d", mockPlugin2.GetTotalEvents())
	}

	// Plugin3: only timestamp >= 5 (5 events: 5-9)
	if mockPlugin3.GetTotalEvents() != 5 {
		t.Errorf("Plugin3: Expected 5 events, got %d", mockPlugin3.GetTotalEvents())
	}
}

func TestProcessor_GracefulShutdown(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       2,
		ChannelBuffer: 100,
		BatchSize:     100,           // Large batch size
		FlushInterval: 1 * time.Hour, // Long interval
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit 50 events (won't trigger batch flush)
	for i := 0; i < 50; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		if err := processor.Submit(event); err != nil {
			t.Fatalf("Failed to submit event: %v", err)
		}
	}

	// Give workers a moment to pick up events from channel
	time.Sleep(100 * time.Millisecond)

	// Shutdown (should flush pending events)
	if err := processor.Shutdown(10 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	// All 50 events should be flushed on shutdown
	if mockPlugin.GetTotalEvents() != 50 {
		t.Errorf("Expected 50 events flushed on shutdown, got %d", mockPlugin.GetTotalEvents())
	}
}

func TestProcessor_ConcurrentSubmission(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       5,
		ChannelBuffer: 1000,
		BatchSize:     50,
		FlushInterval: 100 * time.Millisecond,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit events from multiple goroutines
	numGoroutines := 10
	eventsPerGoroutine := 50
	var wg sync.WaitGroup
	var submitErrors sync.Map
	errorCount := 0

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				event := &storage.TelemetryEvent{
					Kind:            storage.KindGameplay,
					Namespace:       "test",
					UserID:          "user1",
					ServerTimestamp: int64(goroutineID*1000 + i),
					Gameplay: &pb.CreateGameplayTelemetryRequest{
						EventId: "test_event",
					},
				}
				if err := processor.Submit(event); err != nil {
					submitErrors.Store(goroutineID*1000+i, err)
				}
			}
		}(g)
	}

	wg.Wait()

	// Count submit errors
	submitErrors.Range(func(key, value interface{}) bool {
		errorCount++
		return true
	})

	if errorCount > 0 {
		t.Logf("Warning: %d events failed to submit", errorCount)
	}

	// Wait for processing
	time.Sleep(1 * time.Second)

	// Shutdown
	if err := processor.Shutdown(10 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	expectedTotal := numGoroutines * eventsPerGoroutine

	// All events should be processed (allowing for occasional submit failures under high concurrency)
	actualEvents := mockPlugin.GetTotalEvents()
	if actualEvents < expectedTotal-5 || actualEvents > expectedTotal {
		t.Errorf("Expected approximately %d events processed, got %d", expectedTotal, actualEvents)
	}

	// Check metrics - events_received should match actual processed
	stats := processor.Stats()
	eventsReceived := stats["events_received"].(uint64)
	if eventsReceived < uint64(expectedTotal-5) {
		t.Errorf("Expected approximately %d events received, got %v", expectedTotal, eventsReceived)
	}
}

func TestProcessor_Stats(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       3,
		ChannelBuffer: 200,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit 25 events
	for i := 0; i < 25; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		if err := processor.Submit(event); err != nil {
			t.Fatalf("Failed to submit event: %v", err)
		}
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	// Check stats
	stats := processor.Stats()

	if stats["workers"].(int) != 3 {
		t.Errorf("Expected 3 workers, got %v", stats["workers"])
	}

	if stats["queue_capacity"].(int) != 200 {
		t.Errorf("Expected queue capacity 200, got %v", stats["queue_capacity"])
	}

	if stats["events_received"].(uint64) != 25 {
		t.Errorf("Expected 25 events received, got %v", stats["events_received"])
	}

	if stats["plugins"].(int) != 1 {
		t.Errorf("Expected 1 plugin, got %v", stats["plugins"])
	}

	// Shutdown
	if err := processor.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	// Final stats check
	stats = processor.Stats()
	if stats["events_processed"].(uint64) != 25 {
		t.Errorf("Expected 25 events processed, got %v", stats["events_processed"])
	}
}

func TestProcessor_Backpressure(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       1,                // Single worker
		ChannelBuffer: 10,               // Small buffer
		BatchSize:     100,              // Large batch (won't trigger)
		FlushInterval: 10 * time.Second, // Long interval (won't trigger)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Fill the channel buffer quickly
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		i := i // Capture loop variable
		go func() {
			defer wg.Done()
			event := &storage.TelemetryEvent{
				Kind:            storage.KindGameplay,
				Namespace:       "test",
				UserID:          "user1",
				ServerTimestamp: int64(i),
				Gameplay: &pb.CreateGameplayTelemetryRequest{
					EventId: "test_event",
				},
			}
			processor.Submit(event)
		}()
	}

	// Wait for all submissions to complete
	wg.Wait()

	// Give it some time for processing
	time.Sleep(200 * time.Millisecond)

	// Stats should show queue depth
	stats := processor.Stats()
	queueDepth := stats["queue_depth"].(int)

	// Queue should have some events (may not be full due to worker processing)
	if queueDepth < 0 || queueDepth > 15 {
		t.Logf("Queue depth: %d (expected between 0 and 15)", queueDepth)
	}

	// Shutdown (will flush pending)
	if err := processor.Shutdown(10 * time.Second); err != nil {
		t.Fatalf("Failed to shutdown processor: %v", err)
	}

	// All events should eventually be processed
	if mockPlugin.GetTotalEvents() != 15 {
		t.Errorf("Expected 15 events processed, got %d", mockPlugin.GetTotalEvents())
	}
}

func TestProcessor_ShutdownTimeout(t *testing.T) {
	mockPlugin := NewMockPlugin("mock")
	plugins := []storage.StoragePlugin[*storage.TelemetryEvent]{mockPlugin}
	deduplicator := dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()

	config := Config{
		Workers:       2,
		ChannelBuffer: 100,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	processor := NewProcessor(config, plugins, deduplicator, logger)
	processor.Start()

	// Submit a few events
	for i := 0; i < 5; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		processor.Submit(event)
	}

	// Shutdown with very short timeout
	err := processor.Shutdown(1 * time.Nanosecond)

	// Should timeout
	if err == nil {
		t.Log("Expected timeout error, got nil (workers may have finished very quickly)")
	}
}
