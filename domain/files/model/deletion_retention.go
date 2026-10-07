package model

import "time"

// DeletionRetentionSnapshot contains aggregate evidence, never deletion plans.
// Payload JSON text byte estimates exclude row, index and storage overhead.
type DeletionRetentionSnapshot struct {
	ObservedAt                            time.Time                    `json:"observedAt"`
	TotalCount                            int64                        `json:"totalCount"`
	PendingCount                          int64                        `json:"pendingCount"`
	CompletedCount                        int64                        `json:"completedCount"`
	MalformedEnvelopeCount                int64                        `json:"malformedEnvelopeCount"`
	UnrecognizedBindingCount              int64                        `json:"unrecognizedBindingCount"`
	UnknownAgeCount                       int64                        `json:"unknownAgeCount"`
	PayloadJSONTextBytesEstimate          int64                        `json:"payloadJsonTextBytesEstimate"`
	PendingPayloadJSONTextBytesEstimate   int64                        `json:"pendingPayloadJsonTextBytesEstimate"`
	CompletedPayloadJSONTextBytesEstimate int64                        `json:"completedPayloadJsonTextBytesEstimate"`
	OldestPendingCreatedAt                *time.Time                   `json:"oldestPendingCreatedAt"`
	OldestCompletedAt                     *time.Time                   `json:"oldestCompletedAt"`
	Projection                            *DeletionRetentionProjection `json:"projection,omitempty"`
}

// DeletionRetentionProjection counts completed_at strictly before the caller's
// original UTC cutoff, including nanosecond boundaries, with known ages.
// It is a what-if, never permission to mutate a row.
type DeletionRetentionProjection struct {
	CompletedBefore              time.Time `json:"completedBefore"`
	CompletedCount               int64     `json:"completedCount"`
	PayloadJSONTextBytesEstimate int64     `json:"payloadJsonTextBytesEstimate"`
}
