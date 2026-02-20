// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package processor

import (
	"sync"
	"testing"
	"time"

	pb "github.com/agriardyan/extend-game-telemetry-collector/pkg/pb"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"
)

func TestBatcher_SizeBasedFlush(t *testing.T) {
	maxSize := 5
	maxWait := 1 * time.Hour // Set high to ensure size triggers first

	var mu sync.Mutex
	var flushedBatches [][]*storage.TelemetryEvent

	flushFunc := func(batch []*storage.TelemetryEvent) {
		mu.Lock()
		defer mu.Unlock()
		flushedBatches = append(flushedBatches, batch)
	}

	batcher := NewBatcher(maxSize, maxWait, flushFunc)

	// Add events one by one
	for i := 0; i < 12; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		batcher.Add(event)
		time.Sleep(10 * time.Millisecond) // Small delay to ensure flush completes
	}

	// Wait a bit for async flushes
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have 2 full batches of 5 events each
	// (12 events / 5 = 2 full batches + 2 remaining)
	if len(flushedBatches) != 2 {
		t.Errorf("Expected 2 flushed batches, got %d", len(flushedBatches))
	}

	for i, batch := range flushedBatches {
		if len(batch) != maxSize {
			t.Errorf("Batch %d: expected %d events, got %d", i, maxSize, len(batch))
		}
	}

	// Remaining 2 events should still be in the batcher
	if batcher.Size() != 2 {
		t.Errorf("Expected 2 events remaining in batcher, got %d", batcher.Size())
	}
}

func TestBatcher_TimeBasedFlush(t *testing.T) {
	maxSize := 100 // Set high to ensure time triggers first
	maxWait := 200 * time.Millisecond

	var mu sync.Mutex
	var flushedBatches [][]*storage.TelemetryEvent

	flushFunc := func(batch []*storage.TelemetryEvent) {
		mu.Lock()
		defer mu.Unlock()
		flushedBatches = append(flushedBatches, batch)
	}

	batcher := NewBatcher(maxSize, maxWait, flushFunc)

	// Add 3 events
	for i := 0; i < 3; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		batcher.Add(event)
	}

	// Wait for timer to trigger flush
	time.Sleep(maxWait + 100*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have 1 batch with 3 events
	if len(flushedBatches) != 1 {
		t.Errorf("Expected 1 flushed batch, got %d", len(flushedBatches))
	}

	if len(flushedBatches) > 0 && len(flushedBatches[0]) != 3 {
		t.Errorf("Expected 3 events in batch, got %d", len(flushedBatches[0]))
	}

	// Batcher should be empty
	if batcher.Size() != 0 {
		t.Errorf("Expected 0 events remaining in batcher, got %d", batcher.Size())
	}
}

func TestBatcher_ManualFlush(t *testing.T) {
	maxSize := 100
	maxWait := 1 * time.Hour

	var mu sync.Mutex
	var flushedBatches [][]*storage.TelemetryEvent

	flushFunc := func(batch []*storage.TelemetryEvent) {
		mu.Lock()
		defer mu.Unlock()
		flushedBatches = append(flushedBatches, batch)
	}

	batcher := NewBatcher(maxSize, maxWait, flushFunc)

	// Add 5 events
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
		batcher.Add(event)
	}

	// Manually flush
	batcher.Flush()

	// Wait for async flush
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have 1 batch with 5 events
	if len(flushedBatches) != 1 {
		t.Errorf("Expected 1 flushed batch, got %d", len(flushedBatches))
	}

	if len(flushedBatches) > 0 && len(flushedBatches[0]) != 5 {
		t.Errorf("Expected 5 events in batch, got %d", len(flushedBatches[0]))
	}

	// Batcher should be empty
	if batcher.Size() != 0 {
		t.Errorf("Expected 0 events remaining in batcher, got %d", batcher.Size())
	}
}

func TestBatcher_EmptyFlush(t *testing.T) {
	maxSize := 100
	maxWait := 1 * time.Hour

	var mu sync.Mutex
	var flushedBatches [][]*storage.TelemetryEvent

	flushFunc := func(batch []*storage.TelemetryEvent) {
		mu.Lock()
		defer mu.Unlock()
		flushedBatches = append(flushedBatches, batch)
	}

	batcher := NewBatcher(maxSize, maxWait, flushFunc)

	// Flush without adding any events
	batcher.Flush()

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should not have any flushed batches
	if len(flushedBatches) != 0 {
		t.Errorf("Expected 0 flushed batches, got %d", len(flushedBatches))
	}
}

func TestBatcher_ConcurrentAdds(t *testing.T) {
	maxSize := 50
	maxWait := 100 * time.Millisecond

	var mu sync.Mutex
	var flushedBatches [][]*storage.TelemetryEvent
	totalFlushed := 0

	flushFunc := func(batch []*storage.TelemetryEvent) {
		mu.Lock()
		defer mu.Unlock()
		flushedBatches = append(flushedBatches, batch)
		totalFlushed += len(batch)
	}

	batcher := NewBatcher(maxSize, maxWait, flushFunc)

	// Concurrently add events from multiple goroutines
	numGoroutines := 10
	eventsPerGoroutine := 20
	var wg sync.WaitGroup

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
				batcher.Add(event)
			}
		}(g)
	}

	wg.Wait()

	// Flush remaining events
	batcher.Flush()

	// Wait for async flushes
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	expectedTotal := numGoroutines * eventsPerGoroutine

	// Check that all events were flushed
	if totalFlushed != expectedTotal {
		t.Errorf("Expected %d total flushed events, got %d", expectedTotal, totalFlushed)
	}

	// Verify no duplicate events
	eventSet := make(map[int64]bool)
	for _, batch := range flushedBatches {
		for _, event := range batch {
			if eventSet[event.ServerTimestamp] {
				t.Errorf("Duplicate event with server_timestamp %d", event.ServerTimestamp)
			}
			eventSet[event.ServerTimestamp] = true
		}
	}
}

func TestBatcher_TimerCancellation(t *testing.T) {
	maxSize := 5
	maxWait := 200 * time.Millisecond

	var mu sync.Mutex
	var flushedBatches [][]*storage.TelemetryEvent

	flushFunc := func(batch []*storage.TelemetryEvent) {
		mu.Lock()
		defer mu.Unlock()
		flushedBatches = append(flushedBatches, batch)
	}

	batcher := NewBatcher(maxSize, maxWait, flushFunc)

	// Add 3 events (starts timer)
	for i := 0; i < 3; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		batcher.Add(event)
	}

	// Add 2 more events to trigger size-based flush (should cancel timer)
	time.Sleep(50 * time.Millisecond)
	for i := 3; i < 5; i++ {
		event := &storage.TelemetryEvent{
			Kind:            storage.KindGameplay,
			Namespace:       "test",
			UserID:          "user1",
			ServerTimestamp: int64(i),
			Gameplay: &pb.CreateGameplayTelemetryRequest{
				EventId: "test_event",
			},
		}
		batcher.Add(event)
	}

	// Wait for async flush
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should have 1 batch (size-based flush)
	if len(flushedBatches) != 1 {
		t.Errorf("Expected 1 flushed batch, got %d", len(flushedBatches))
	}

	// Wait to ensure timer doesn't fire
	time.Sleep(maxWait + 100*time.Millisecond)

	// Still should have only 1 batch (timer was cancelled)
	if len(flushedBatches) != 1 {
		t.Errorf("Expected 1 flushed batch after timer wait, got %d", len(flushedBatches))
	}
}
