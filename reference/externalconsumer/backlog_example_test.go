package externalconsumer_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/assurrussa/outbox/outbox"

	"github.com/assurrussa/gouploads/host"
)

// This adapter belongs to the host, not the GoUploads runtime. A callback can
// set gauges in the host's existing metrics system without a new SDK dependency.
type backlogSample struct {
	QueueObservedAt    time.Time
	DeletionObservedAt time.Time
	Jobs               int64
	UploadJobs         int64
	PendingDeletions   int64
}

type backlogQueueReader interface {
	GetQueueStats(context.Context) (outbox.QueueStats, error)
}

var _ backlogQueueReader = (*outbox.Service)(nil)

func collectBacklog(
	ctx context.Context,
	queue backlogQueueReader,
	files host.DeletionRetentionReader,
	emit func(backlogSample),
) error {
	stats, err := queue.GetQueueStats(ctx)
	if err != nil {
		return err
	}
	inventory, err := host.InspectDeletionRetention(ctx, files, nil)
	if err != nil {
		return err
	}
	sample := backlogSample{
		QueueObservedAt:    stats.ObservedAt,
		DeletionObservedAt: inventory.Snapshot.ObservedAt,
		Jobs:               stats.Total,
		PendingDeletions:   inventory.Snapshot.PendingCount,
	}
	for _, group := range stats.ByCapability {
		// Fixed library job names; sum all schema versions, including work the
		// current workers cannot claim. These are jobs, not unique files.
		switch group.Name {
		case "finalize_original_file", "send_resize_file", "listen_resize_file", "upload_file_persist":
			sample.UploadJobs += group.Total
		}
	}
	emit(sample)
	return nil
}

type exampleBacklogQueue struct {
	stats outbox.QueueStats
	err   error
}

func (q exampleBacklogQueue) GetQueueStats(context.Context) (outbox.QueueStats, error) {
	return q.stats, q.err
}

type exampleBacklogFiles struct {
	snapshot host.DeletionRetentionSnapshot
	err      error
}

func (f exampleBacklogFiles) GetDeletionRetention(
	context.Context, *time.Time,
) (host.DeletionRetentionSnapshot, error) {
	return f.snapshot, f.err
}

func Example_backlogMetrics() {
	queue := exampleBacklogQueue{stats: outbox.QueueStats{
		Total: 4,
		ByCapability: []outbox.CapabilityQueueStats{
			{Name: "finalize_original_file", SchemaVersion: 1, Total: 2},
			{Name: "send_email", SchemaVersion: 1, Total: 2},
		},
	}}
	files := exampleBacklogFiles{snapshot: host.DeletionRetentionSnapshot{PendingCount: 1}}
	// Production: pass the host's Outbox service and runtime.Files after
	// authorizing their entire database scope. No scheduler is started here.
	err := collectBacklog(context.Background(), queue, files, func(sample backlogSample) {
		fmt.Println("jobs:", sample.Jobs,
			"upload jobs:", sample.UploadJobs, "pending deletions:", sample.PendingDeletions)
	})
	if err != nil {
		panic(err)
	}
	// Output: jobs: 4 upload jobs: 2 pending deletions: 1
}

func TestBacklogExampleSnapshots(t *testing.T) {
	queueAt := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	deletionAt := queueAt.Add(time.Second)
	queue := exampleBacklogQueue{stats: outbox.QueueStats{
		ObservedAt: queueAt, Total: 20,
		ByCapability: []outbox.CapabilityQueueStats{
			{Name: "finalize_original_file", SchemaVersion: 1, Total: 2},
			{Name: "finalize_original_file", SchemaVersion: 99, Total: 3},
			{Name: "send_resize_file", SchemaVersion: 1, Total: 4},
			{Name: "listen_resize_file", SchemaVersion: 1, Total: 2},
			{Name: "upload_file_persist", SchemaVersion: 1, Total: 1},
			{Name: "deleted_file", SchemaVersion: 1, Total: 4},
			{Name: "send_email", SchemaVersion: 1, Total: 4},
		},
	}}
	files := exampleBacklogFiles{snapshot: host.DeletionRetentionSnapshot{ObservedAt: deletionAt, PendingCount: 7}}
	want := backlogSample{
		QueueObservedAt: queueAt, DeletionObservedAt: deletionAt,
		Jobs: 20, UploadJobs: 12, PendingDeletions: 7,
	}
	calls := 0
	if err := collectBacklog(t.Context(), queue, files, func(got backlogSample) {
		calls++
		if got != want {
			t.Errorf("sample = %+v, want %+v", got, want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("callback calls = %d, want 1", calls)
	}
}

func TestBacklogExampleUnavailableIsNotZero(t *testing.T) {
	failure := errors.New("unavailable")
	for _, scenario := range []struct {
		name  string
		queue exampleBacklogQueue
		files exampleBacklogFiles
	}{
		{name: "queue", queue: exampleBacklogQueue{err: failure}},
		{name: "deletions", files: exampleBacklogFiles{err: failure}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			err := collectBacklog(t.Context(), scenario.queue, scenario.files, func(backlogSample) {
				t.Error("unavailable evidence must not emit zero gauges")
			})
			if err == nil {
				t.Fatal("expected source error")
			}
		})
	}
	called := false
	err := collectBacklog(t.Context(), exampleBacklogQueue{}, exampleBacklogFiles{}, func(sample backlogSample) {
		called = true
		if sample != (backlogSample{}) {
			t.Errorf("empty sources: %+v", sample)
		}
	})
	if err != nil || !called {
		t.Fatalf("successful empty snapshot: called=%v error=%v", called, err)
	}
}
