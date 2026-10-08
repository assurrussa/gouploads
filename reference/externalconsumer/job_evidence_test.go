package externalconsumer_test

import (
	"context"
	"testing"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"
)

var _ host.FileJobReader = (*host.FileRepo)(nil)
var _ host.UploadOutbox = (*host.PostgresFileJobOutbox)(nil)
var _ func(*pgsqlclient.Client) (*host.PostgresFileJobOutbox, error) = host.NewPostgresFileJobOutbox

func TestFileJobEvidenceUsesSupportedHostSurface(t *testing.T) {
	_, err := host.InspectFileJobs(context.Background(), nil, 1, host.FileJobOriginalFinalization)
	if err != host.ErrDiagnosticUnavailable {
		t.Fatalf("expected unavailable, got %v", err)
	}
}
