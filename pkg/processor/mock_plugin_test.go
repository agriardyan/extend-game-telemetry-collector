// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package processor

import (
	"context"
	"sync"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"
)

// MockPlugin is a test storage plugin that tracks writes
type MockPlugin struct {
	name         string
	mu           sync.Mutex
	writeCalls   int
	totalEvents  int
	batches      [][]*storage.TelemetryEvent
	filterFunc   func(*storage.TelemetryEvent) bool
	shouldFail   bool
	failureError error
}

func NewMockPlugin(name string) *MockPlugin {
	return &MockPlugin{
		name:       name,
		batches:    make([][]*storage.TelemetryEvent, 0),
		filterFunc: func(*storage.TelemetryEvent) bool { return true },
	}
}

func (m *MockPlugin) Name() string {
	return m.name
}

func (m *MockPlugin) Initialize(ctx context.Context) error {
	return nil
}

func (m *MockPlugin) Filter(event *storage.TelemetryEvent) bool {
	return m.filterFunc(event)
}

func (m *MockPlugin) transform(event *storage.TelemetryEvent) (interface{}, error) {
	return event, nil
}

func (m *MockPlugin) WriteBatch(ctx context.Context, events []*storage.TelemetryEvent) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.writeCalls++
	m.totalEvents += len(events)

	// Copy the batch to avoid data races
	batch := make([]*storage.TelemetryEvent, len(events))
	copy(batch, events)
	m.batches = append(m.batches, batch)

	if m.shouldFail {
		return 0, m.failureError
	}

	return len(events), nil
}

func (m *MockPlugin) Close() error {
	return nil
}

func (m *MockPlugin) HealthCheck(ctx context.Context) error {
	return nil
}

// Test helper methods

func (m *MockPlugin) SetFilter(f func(*storage.TelemetryEvent) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filterFunc = f
}

func (m *MockPlugin) SetShouldFail(shouldFail bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = shouldFail
	m.failureError = err
}

func (m *MockPlugin) GetWriteCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeCalls
}

func (m *MockPlugin) GetTotalEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalEvents
}

func (m *MockPlugin) GetBatches() [][]*storage.TelemetryEvent {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return a copy to avoid data races
	batches := make([][]*storage.TelemetryEvent, len(m.batches))
	copy(batches, m.batches)
	return batches
}

func (m *MockPlugin) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.writeCalls = 0
	m.totalEvents = 0
	m.batches = make([][]*storage.TelemetryEvent, 0)
}
