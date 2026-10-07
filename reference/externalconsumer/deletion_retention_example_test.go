package externalconsumer_test

import (
	"context"
	"fmt"
	"time"

	"github.com/assurrussa/gouploads/host"
)

// An example host-authorized aggregate reader; production hosts can use FileRepo.
type exampleRetentionReader struct{}

func (exampleRetentionReader) GetDeletionRetention(_ context.Context, cutoff *time.Time) (host.DeletionRetentionSnapshot, error) {
	snapshot := host.DeletionRetentionSnapshot{TotalCount: 3, PendingCount: 1, CompletedCount: 2}
	if cutoff != nil {
		snapshot.Projection = &host.DeletionRetentionProjection{
			CompletedBefore: *cutoff, CompletedCount: 1,
			PayloadJSONTextBytesEstimate: 128,
		}
	}
	return snapshot, nil
}

func ExampleInspectDeletionRetention() {
	// The host must authorize the inventory's entire scope before this call.
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inventory, err := host.InspectDeletionRetention(context.Background(), exampleRetentionReader{}, &cutoff)
	if err != nil {
		fmt.Println("inventory unavailable")
		return
	}
	snapshot := inventory.Snapshot
	fmt.Println(inventory.Source, snapshot.PendingCount, snapshot.CompletedCount)
	fmt.Println("age what-if:", snapshot.Projection.CompletedCount)
	fmt.Println("JSON text byte estimate:", snapshot.Projection.PayloadJSONTextBytesEstimate)
	// Output:
	// present 1 2
	// age what-if: 1
	// JSON text byte estimate: 128
}
