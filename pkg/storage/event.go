// Copyright (c) 2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package storage

import (
	"fmt"

	pb "github.com/agriardyan/extend-game-telemetry-collector/pkg/pb"
)

// Kind identifies which telemetry category an event belongs to.
type Kind string

const (
	KindUserBehavior Kind = "user_behavior"
	KindGameplay     Kind = "gameplay"
	KindPerformance  Kind = "performance"
)

// TelemetryEvent is the canonical in-memory representation of a telemetry event.
// Exactly one of UserBehavior, Gameplay, or Performance will be non-nil,
// matching the value of Kind.
type TelemetryEvent struct {
	// Kind identifies the telemetry category
	Kind Kind

	// Namespace is the game namespace, injected server-side (not from client)
	Namespace string

	// UserID extracted from the JWT auth token
	UserID string

	// ServerTimestamp is the Unix timestamp (milliseconds) when the server received the event
	ServerTimestamp int64

	// SourceIP is the client's IP address
	SourceIP string

	// Payloads — only one is set, determined by Kind
	UserBehavior *pb.CreateUserBehaviorTelemetryRequest
	Gameplay     *pb.CreateGameplayTelemetryRequest
	Performance  *pb.CreatePerformanceTelemetryRequest
}

// ToDocument returns the event as a flat map suitable for JSON/BSON serialization.
// The "payload" key holds the full typed request struct.
func (e *TelemetryEvent) ToDocument() map[string]interface{} {
	doc := map[string]interface{}{
		"kind":             string(e.Kind),
		"namespace":        e.Namespace,
		"user_id":          e.UserID,
		"server_timestamp": e.ServerTimestamp,
		"source_ip":        e.SourceIP,
	}
	switch e.Kind {
	case KindUserBehavior:
		if e.UserBehavior != nil {
			doc["event_id"] = e.UserBehavior.EventId
			doc["version"] = e.UserBehavior.Version
			doc["timestamp"] = e.UserBehavior.Timestamp
			doc["payload"] = e.UserBehavior
		}
	case KindGameplay:
		if e.Gameplay != nil {
			doc["event_id"] = e.Gameplay.EventId
			doc["version"] = e.Gameplay.Version
			doc["timestamp"] = e.Gameplay.Timestamp
			doc["payload"] = e.Gameplay
		}
	case KindPerformance:
		if e.Performance != nil {
			doc["event_id"] = e.Performance.EventId
			doc["version"] = e.Performance.Version
			doc["timestamp"] = e.Performance.Timestamp
			doc["payload"] = e.Performance
		}
	}
	return doc
}

// DeduplicationKey returns a stable string used for deduplication.
// It incorporates enough fields to uniquely identify a single event submission.
func (e *TelemetryEvent) DeduplicationKey() string {
	switch e.Kind {
	case KindUserBehavior:
		sessionID := ""
		if e.UserBehavior.Context != nil {
			sessionID = e.UserBehavior.Context.SessionId
		}
		return fmt.Sprintf("%s:%s:%s:%s:%s",
			e.Kind, e.Namespace, e.UserID,
			e.UserBehavior.EventId,
			sessionID)

	case KindGameplay:
		return fmt.Sprintf("%s:%s:%s:%s:%s",
			e.Kind, e.Namespace, e.UserID,
			e.Gameplay.EventId,
			e.Gameplay.Timestamp)

	case KindPerformance:
		return fmt.Sprintf("%s:%s:%s:%s:%s",
			e.Kind, e.Namespace, e.UserID,
			e.Performance.EventId,
			e.Performance.Timestamp)

	default:
		return fmt.Sprintf("%s:%s:%s", e.Kind, e.Namespace, e.UserID)
	}
}
