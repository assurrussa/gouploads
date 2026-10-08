package host

import (
	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"

	"github.com/assurrussa/gouploads/domain/files/service/filejoboutbox"
)

// PostgresFileJobOutbox is an opt-in producer with atomic queue associations.
type PostgresFileJobOutbox = filejoboutbox.Outbox

// NewPostgresFileJobOutbox uses the pinned PostgreSQL Outbox implementation.
// Pass it as the Outbox to upload, media and deletion constructors; use the SAME
// database and pinned transaction context for file metadata and worker wiring.
// Custom queue adapters are not silently replaced. Construction performs no I/O,
// worker registration or migration. Apply both libraries' migrations first.
func NewPostgresFileJobOutbox(database *pgsqlclient.Client) (*PostgresFileJobOutbox, error) {
	return filejoboutbox.New(database)
}
