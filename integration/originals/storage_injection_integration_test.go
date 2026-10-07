//go:build integration

package originals_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
)

// The protocol spool and persistence root deliberately differ. A local TUS
// completion must pass bytes to the injected store, never reuse its spool key.
func TestIntegrationOriginalStorageInjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, _, cleanup := hosttest.PrepareDB(ctx, t, "storage_injection")
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
	persistenceCfg := cfg
	persistenceCfg.Local.Root = t.TempDir()
	base, err := host.NewStorage(persistenceCfg)
	require.NoError(t, err)
	supplied := &instrumentedOriginalStorage{Storage: base}
	body := []byte("%PDF-1.7\ninjected persistence integration payload\n%%EOF\n")
	scanner := host.ContentScannerFunc(func(_ context.Context, reader io.Reader, _ host.ContentScanMetadata) error {
		actual, readErr := io.ReadAll(reader)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(body, actual) {
			return errors.New("scanner received unexpected bytes")
		}
		supplied.scans.Add(1)
		return nil
	})
	queue := newQueue(t, database)
	runtime, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{
		Database: database, Transaction: transaction.New(database.DB()), Outbox: queue,
		Storage: supplied, ContentScanner: scanner,
	})
	require.NoError(t, err)
	require.Same(t, supplied, runtime.Storage)
	user := host.NewUserID()
	first, err := runtime.Uploader.UploadReader(ctx,
		host.ReaderRequest{UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadConfig()},
		host.ReaderUploadInput{OriginalName: "injected.pdf", Size: int64(len(body)), Reader: bytes.NewReader(body)})
	require.NoError(t, err)
	resumed := tusUpload(t, ctx, runtime, user, body)
	require.EqualValues(t, 2, supplied.staged.Load(), "reader and TUS completion both stage through supplied storage")
	require.NoError(t, queue.RegisterJobs(runtime.Jobs...))
	stop := startWorker(t, ctx, queue)
	waitEmpty(t, ctx, queue)
	require.EqualValues(t, 2, supplied.scans.Load())
	require.EqualValues(t, 2, supplied.persisted.Load())
	require.GreaterOrEqual(t, supplied.opened.Load(), int64(2), "finalizer source/scanner use supplied storage")
	for _, file := range []host.File{first, resumed} {
		verifyComplete(t, ctx, runtime, file, body)
		require.NoError(t, runtime.Uploader.DeleteFile(ctx, host.DeleteRequest{UserRequestID: user, FileID: file.ID}))
	}
	waitEmpty(t, ctx, queue)
	stop()
	for _, file := range []host.File{first, resumed} {
		deleted, readErr := runtime.Files.GetByID(ctx, file.ID)
		require.NoError(t, readErr)
		require.Zero(t, deleted.ID)
	}
	require.GreaterOrEqual(t, supplied.deleted.Load(), int64(4), "staging cleanup and final deletion use supplied storage")
	require.Zero(t, supplied.closed.Load(), "caller retains resource ownership after worker shutdown")
	// Injecting persistence cannot bypass scanner denial or remove retry evidence.
	deniedBody := []byte("%PDF-1.7\nscanner-denied payload\n%%EOF\n")
	denied, err := runtime.Uploader.UploadReader(ctx,
		host.ReaderRequest{UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadConfig()},
		host.ReaderUploadInput{OriginalName: "denied.pdf", Size: int64(len(deniedBody)), Reader: bytes.NewReader(deniedBody)})
	require.NoError(t, err)
	require.Error(t, runtime.Finalizer.HandleOriginal(ctx, denied.ID))
	require.EqualValues(t, 2, supplied.persisted.Load(), "scanner denial prevents final persistence")
	retained, err := runtime.Files.GetByID(ctx, denied.ID)
	require.NoError(t, err)
	require.Equal(t, host.FileUploadTaskStatusQueued, retained.GetData().Uploader.Status)
	require.Empty(t, retained.GetData().Presets)
	requireBytes(t, ctx, supplied, denied.GetFullPath(), deniedBody)
}

type instrumentedOriginalStorage struct {
	host.Storage
	staged, persisted, opened, deleted, scans, closed atomic.Int64
}

func (s *instrumentedOriginalStorage) SaveTemp(ctx context.Context, input host.SaveFileInput) (host.StoredFile, error) {
	s.staged.Add(1)
	return s.Storage.SaveTemp(ctx, input)
}

func (s *instrumentedOriginalStorage) SavePersist(ctx context.Context, input host.SaveFileInput) (host.StoredFile, error) {
	if s.scans.Load() == 0 {
		return host.StoredFile{}, errors.New("publication attempted before content scan")
	}
	s.persisted.Add(1)
	return s.Storage.SavePersist(ctx, input)
}

func (s *instrumentedOriginalStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	s.opened.Add(1)
	return s.Storage.Open(ctx, key)
}

func (s *instrumentedOriginalStorage) DeleteBatch(ctx context.Context, keys []string) error {
	s.deleted.Add(int64(len(keys)))
	return s.Storage.DeleteBatch(ctx, keys)
}

func (s *instrumentedOriginalStorage) Delete(ctx context.Context, key string) error {
	s.deleted.Add(1)
	return s.Storage.Delete(ctx, key)
}

func (s *instrumentedOriginalStorage) Close() error {
	s.closed.Add(1)
	return nil
}
