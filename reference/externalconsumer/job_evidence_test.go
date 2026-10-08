package externalconsumer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"

	"github.com/assurrussa/gouploads/host"
)

var (
	_ host.FileJobReader                                             = (*host.FileRepo)(nil)
	_ host.UploadOutbox                                              = (*host.PostgresFileJobOutbox)(nil)
	_ func(*pgsqlclient.Client) (*host.PostgresFileJobOutbox, error) = host.NewPostgresFileJobOutbox
)

func TestFileJobEvidenceUsesSupportedHostSurface(t *testing.T) {
	_, err := host.InspectFileJobs(context.Background(), nil, 1, host.FileJobOriginalFinalization)
	if !errors.Is(err, host.ErrDiagnosticUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}
