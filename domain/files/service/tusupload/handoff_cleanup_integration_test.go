//go:build integration

//nolint:testpackage // exercises the private cleanup claim against the public file repository.
package tusupload

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsrepo"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/assurrussa/outbox/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	"github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/internal/pointer"
)

// The first operation holds its real transaction open until PostgreSQL reports
// the other operation waiting on an advisory lock. No scheduling delay decides
// the winner, and each outcome includes the actual file/outbox handoff state.
func TestIntegrationHandoffCleanupSerializesCommitOrder(t *testing.T) {
	for _, handoffFirst := range []bool{true, false} {
		name := "cleanup_first"
		if handoffFirst {
			name = "handoff_first"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			database, _, cleanup := testshelpers.PrepareDB(ctx, t, "handoffcleanup")
			defer cleanup(context.Background())
			// A failed assertion must not leave a cleaning row blocking migration reset.
			defer func() {
				_, err := database.DB().Exec(context.Background(), "remove_cleanup_sessions", "delete from upload_sessions")
				require.NoError(t, err)
			}()
			prepareHandoffOutbox(t, ctx, database)
			sessions, err := newPostgresSessionRepository(database)
			require.NoError(t, err)
			tx := transaction.New(database.DB())
			files := filerepo.Must(filerepo.NewOptions(database, tx))
			jobs := jobsrepo.Must(jobsrepo.NewOptions(database))
			session := expiredHandoffSession()
			require.NoError(t, sessions.Create(ctx, session))
			var calls atomic.Int64
			create := func(txCtx context.Context) (model.File, error) {
				calls.Add(1)
				return createHandoffFileAndJob(txCtx, files, jobs, session)
			}
			harness := handoffCleanupHarness{database: database, tx: tx, files: files, sessions: sessions}
			runHandoffCleanupOrder(t, ctx, harness, session, create, handoffFirst)
			want := 0
			if handoffFirst {
				want = 1
			}
			require.EqualValues(t, want, calls.Load(), "cleanup winner must prevent the creation callback")
			for _, table := range []string{"files", "jobs", "upload_finalizations"} {
				var count int
				query := "select count(*) from " + table // Test-owned fixed table names only.
				require.NoError(t, database.DB().QueryRow(ctx, "count_handoff_effects", query).Scan(&count))
				require.Equal(t, want, count, "unexpected rows in %s", table)
			}
		})
	}
}

func prepareHandoffOutbox(t *testing.T, ctx context.Context, database pgsql.Client) {
	t.Helper()
	sqlDB := stdlib.OpenDBFromPool(database.DB().Pool())
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithTableName("outbox_schema_versions"),
	)
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
}

func expiredHandoffSession() s3Session {
	now := time.Now().UTC()
	return s3Session{
		Session: Session{
			ID: uuid.NewString(), UploadLength: 12, Offset: 12,
			Metadata: map[string]string{"entity_type": "exercise", "entity_id": "9004"},
			Path:     "staging/v1/tus/source.png", OriginalName: "source.png", FileName: "source.png",
			MimeType: "image/png", OwnerUUID: identity.NewUserID(),
			CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
			Status: StatusReady, Quarantined: true, FinalizationKey: uuid.NewString(),
		},
		Parts: make(map[int32]durablePart), ExpiresAt: now.Add(-time.Hour),
	}
}

func createHandoffFileAndJob(
	ctx context.Context, files *filerepo.Repo, jobs *jobsrepo.Repo, session s3Session,
) (model.File, error) {
	now := time.Now().UTC()
	file := model.File{
		ObjectType: shared.ObjectTypeExercise, ObjectID: pointer.To(shared.FileObjectID(9004)),
		OriginalFileName: session.OriginalName, FileName: session.FileName, Size: session.UploadLength,
		MimeType: session.MimeType, FileType: model.FileTypeImage, FolderPath: "staging/v1/tus",
		Slug: uuid.NewString(), CreatedAt: now, UpdatedAt: now,
	}
	id, err := files.Create(ctx, file)
	if err != nil {
		return model.File{}, err
	}
	file.ID = id
	_, err = jobs.CreateJobVersioned(ctx, "finalize_original_file", outbox.DefaultSchemaVersion,
		fmt.Sprintf(`{"fileId":%d}`, id), now,
	)
	return file, err
}

type handoffCleanupHarness struct {
	database pgsql.Client
	tx       *transaction.Manager
	files    *filerepo.Repo
	sessions *postgresSessionRepository
}

func runHandoffCleanupOrder(
	t *testing.T, ctx context.Context, harness handoffCleanupHarness, session s3Session,
	create func(context.Context) (model.File, error), handoffFirst bool,
) {
	t.Helper()
	ready := make(chan error, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	allowCommit := func() { releaseOnce.Do(func() { close(release) }) }
	defer allowCommit()
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	var claimed cleanupClaim
	finalize := func(txCtx context.Context) error {
		_, err := harness.files.FinalizeUpload(txCtx, session.FinalizationKey, strings.Repeat("a", 64), true, create)
		return err
	}
	claim := func(txCtx context.Context) error {
		var err error
		claimed, err = harness.sessions.ClaimCleanup(txCtx, session.ID, time.Now().UTC(), uuid.NewString(), time.Minute)
		return err
	}
	first, second := claim, finalize
	if handoffFirst {
		first, second = finalize, claim
	}
	go func() {
		firstDone <- harness.tx.RunInTx(ctx, func(txCtx context.Context) error {
			err := first(txCtx)
			ready <- err
			if err != nil {
				return err
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	require.NoError(t, receiveHandoffResult(ctx, ready))
	go func() { secondDone <- second(ctx) }()
	require.NoError(t, awaitHandoffAdvisoryWaiter(ctx, harness.database))
	allowCommit()
	require.NoError(t, receiveHandoffResult(ctx, firstDone))
	err := receiveHandoffResult(ctx, secondDone)
	if handoffFirst {
		require.NoError(t, err)
		require.True(t, claimed.KeepObject, "committed handoff transfers source ownership away from cleanup")
	} else {
		require.ErrorIs(t, err, model.ErrFinalizationConflict)
		require.False(t, claimed.KeepObject, "cleanup claimed the source before any handoff existed")
	}
	require.Equal(t, StatusCleaning, claimed.Session.Status)
	require.NoError(t, harness.sessions.DeleteClaimed(ctx, session.ID, claimed.Claim))
}

func receiveHandoffResult(ctx context.Context, result <-chan error) error {
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func awaitHandoffAdvisoryWaiter(ctx context.Context, database pgsql.Client) error {
	const query = `select exists (
    select 1 from pg_locks
    where locktype = 'advisory' and not granted
      and database = (select oid from pg_database where datname = current_database())
)`
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var waiting bool
		if err := database.DB().QueryRow(ctx, "handoff_advisory_waiter", query).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
	}
}
