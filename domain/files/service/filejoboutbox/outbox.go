// Package filejoboutbox provides opt-in producer associations for pinned PostgreSQL Outbox.
package filejoboutbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsrepo"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	core "github.com/assurrussa/outbox/outbox"
	"github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// Outbox is a producer, not a worker or a replacement for worker registration.
// The concrete pinned client is intentional: arbitrary Put implementations do
// not prove shared transaction/connection semantics.
type Outbox struct {
	jobs interface {
		CreateJobVersioned(context.Context, string, core.SchemaVersion, string, time.Time) (types.JobID, error)
	}
	tx interface {
		RunInTx(context.Context, func(context.Context) error) error
	}
}

func New(database *pgsqlclient.Client) (*Outbox, error) {
	if database == nil || database.DB() == nil {
		return nil, errors.New("file job outbox requires a PostgreSQL client")
	}
	jobs, err := jobsrepo.New(jobsrepo.NewOptions(database))
	if err != nil {
		return nil, err
	}
	return &Outbox{jobs: jobs, tx: transaction.New(database.DB())}, nil
}

func (o *Outbox) Put(ctx context.Context, name, payload string, availableAt time.Time) (types.JobID, error) {
	return o.jobs.CreateJobVersioned(ctx, name, core.DefaultSchemaVersion, payload, availableAt)
}

func (o *Outbox) PutFileJob(ctx context.Context, file model.File, operation model.FileJobOperation,
	name, payload string, availableAt time.Time,
) (types.JobID, error) {
	if file.ID <= 0 || file.Slug == "" || !operation.Valid() {
		return types.JobIDNil, errors.New("invalid file job binding")
	}
	if err := ctx.Err(); err != nil {
		return types.JobIDNil, err
	}
	var id types.JobID
	err := o.tx.RunInTx(ctx, func(txCtx context.Context) error {
		tx := pgsql.GetTx(txCtx)
		if tx == nil {
			return errors.New("file job association requires a transaction")
		}
		// Hold the immutable binding through enqueue and association persistence.
		var exists bool
		err := tx.QueryRow(txCtx, `select true from files
where id = $1 and slug = $2 and object_type = $3
and object_id is not distinct from $4 and deleted_at is null for share`,
			file.ID, file.Slug, file.ObjectType.String(), file.ObjectID).Scan(&exists)
		if err != nil {
			return fmt.Errorf("verify file job binding: %w", err)
		}
		id, err = o.Put(txCtx, name, payload, availableAt)
		if err != nil {
			return err
		}
		// jobsrepo.Create -> pgsqlclient.Getx/QueryRow uses this same GetTx value.
		// No foreign key to jobs/files: successful acknowledgments and file
		// deletion must not erase provenance.
		_, err = tx.Exec(txCtx, `insert into file_job_associations
(job_id, file_id, generation, object_type, object_id, operation, job_name, schema_version)
values ($1, $2, $3, $4, $5, $6, $7, $8)`,
			id, file.ID, file.Slug, file.ObjectType.String(), file.ObjectID,
			string(operation), name, int(core.DefaultSchemaVersion))
		return err
	})
	if err != nil {
		// Includes rollback/cancellation/uncertain commit. Never return an ID as
		// accepted and never compensate by deleting storage or enqueueing again.
		return types.JobIDNil, err
	}
	return id, nil
}
