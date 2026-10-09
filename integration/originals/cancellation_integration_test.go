//go:build integration

package originals_test

import (
	"bytes"
	"context"
	"path"
	"sync/atomic"
	"testing"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	s3storemocks "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store/mocks"
)

// Reuse the originals fixture with a multipart persistence adapter. A blocked
// abort must finish within its own budget, release the real PostgreSQL row lock,
// and leave staging, queued metadata and the original job available for retry.
func TestIntegrationOriginalCancellationReleasesLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, _, cleanup := hosttest.PrepareDB(ctx, t, "original_cancel")
	defer cleanup(context.Background())
	pool, ok := database.DB().(storage.DBPgxEnginePool)
	require.True(t, ok)
	sqlDB := stdlib.OpenDBFromPool(pool.Pool())
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithTableName("outbox_schema_versions"))
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	cfg := storageConfig(t, ctx, host.StorageDriverLocal)
	base, err := host.NewStorage(cfg)
	require.NoError(t, err)
	client := s3storemocks.NewMockMultiPartUploader(gomock.NewController(t))
	supplied := &multipartOriginalStorage{Storage: base, client: client}
	events := &originalCancelEvents{}
	queue := newQueue(t, database)
	runtime, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{
		Database: database, Transaction: transaction.New(database.DB()), Outbox: queue,
		Storage: supplied, Events: events,
	})
	require.NoError(t, err)
	body := []byte("%PDF-1.7\ncancellation integration payload\n%%EOF\n")
	user := host.NewUserID()
	file, err := runtime.Uploader.UploadReader(ctx, host.ReaderRequest{
		UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadConfig(),
		AfterJobs: host.NewFileEventAfterJobs("after_original", user),
	}, host.ReaderUploadInput{OriginalName: "cancel.pdf", Size: int64(len(body)), Reader: bytes.NewReader(body)})
	require.NoError(t, err)
	initialEvents := events.count.Load()
	var initialJobs int
	err = pool.Pool().QueryRow(ctx, "SELECT count(*) FROM jobs").Scan(&initialJobs)
	require.NoError(t, err)
	require.Positive(t, initialJobs)
	started := make(chan struct{})
	// Send the cleanup context to the test so a missing deadline fails promptly,
	// rather than leaving the test blocked on an unbounded detached context.
	aborting := make(chan context.Context, 1)
	client.EXPECT().CreateMultipartUpload(gomock.Any(), gomock.Any()).Return(&s3.CreateMultipartUploadOutput{}, nil)
	client.EXPECT().UploadPart(gomock.Any(), gomock.Any()).DoAndReturn(
		func(uploadCtx context.Context, _ *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
			close(started)
			<-uploadCtx.Done()
			return nil, uploadCtx.Err()
		})
	client.EXPECT().AbortMultipartUpload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(abortCtx context.Context, _ *s3.AbortMultipartUploadInput,
			_ ...func(*s3.Options),
		) (*s3.AbortMultipartUploadOutput, error) {
			aborting <- abortCtx
			select {
			case <-abortCtx.Done():
				return nil, abortCtx.Err()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
	uploadCtx, stopUpload := context.WithCancel(ctx)
	defer stopUpload()
	finalized := make(chan error, 1)
	go func() { finalized <- runtime.Finalizer.HandleOriginal(uploadCtx, file.ID) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	locked := make(chan error, 1)
	go func() {
		tx, lockErr := database.DB().BeginTx(ctx, pgx.TxOptions{})
		if lockErr != nil {
			locked <- lockErr
			return
		}
		defer func() {
			rollbackCtx, stopRollback := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer stopRollback()
			rollbackErr := tx.Rollback(rollbackCtx)
			if lockErr == nil {
				lockErr = rollbackErr
			}
			locked <- lockErr
		}()
		_, lockErr = tx.Exec(ctx, "SELECT id FROM files WHERE id=$1 FOR UPDATE", file.ID)
	}()
	waitLocks(t, ctx, database, 1)
	stopUpload()
	select {
	case abortCtx := <-aborting:
		require.NoError(t, abortCtx.Err(), "abort begins despite upload cancellation")
		deadline, finite := abortCtx.Deadline()
		require.True(t, finite)
		require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err = <-finalized:
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, context.DeadlineExceeded, "cleanup timeout remains visible")
	case <-time.After(7 * time.Second):
		t.Fatal("cancelled finalizer did not finish bounded cleanup")
	}
	select {
	case err = <-locked:
		require.NoError(t, err, "cancellation releases the contested row lock")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	retained, err := runtime.Files.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, file.GetData(), retained.GetData(), "no successful metadata or preset is committed")
	require.Equal(t, host.FileUploadTaskStatusQueued, retained.GetData().Uploader.Status)
	var remainingJobs int
	err = pool.Pool().QueryRow(ctx, "SELECT count(*) FROM jobs").Scan(&remainingJobs)
	require.NoError(t, err)
	require.Equal(t, initialJobs, remainingJobs, "no completion or cleanup jobs are enqueued")
	require.Equal(t, initialEvents, events.count.Load(), "no completion event is published")
	requireBytes(t, ctx, supplied, file.GetFullPath(), body)
}

type multipartOriginalStorage struct {
	host.Storage
	client s3store.MultiPartUploader
}

func (s *multipartOriginalStorage) SavePersist(ctx context.Context, input host.SaveFileInput) (host.StoredFile, error) {
	err := s3store.Upload(ctx, s.client, &s3.CreateMultipartUploadInput{
		Bucket: aws.String("owned-test"), Key: aws.String(path.Join(input.Dir, input.FileName)),
	}, input.Reader)
	return host.StoredFile{}, err
}

type originalCancelEvents struct{ count atomic.Int64 }

func (e *originalCancelEvents) Publish(_ context.Context, _ host.UserID, _ host.Event) error {
	e.count.Add(1)
	return nil
}
