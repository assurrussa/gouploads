//go:build integration

package filerepo_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/internal/pointer"
)

func hardeningFile(objectID int64) model.File {
	return model.File{
		ObjectType:       shared.ObjectTypeExercise,
		ObjectID:         pointer.To(shared.FileObjectID(objectID)),
		OriginalFileName: "source.png",
		FileName:         "source.png",
		MimeType:         "image/png",
		FileType:         model.FileTypeImage,
		Size:             12,
		Slug:             uuid.NewString(),
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
}

func TestIntegrationHardeningFinalizationIsAtomicAndIdempotent(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	_, err := ts.db.DB().Exec(ctx, "create_probe_jobs", "create table probe_jobs (file_id bigint not null)")
	require.NoError(t, err)
	key := uuid.NewString()
	binding := strings.Repeat("a", 64)
	var calls atomic.Int64
	create := func(txCtx context.Context) (model.File, error) {
		calls.Add(1)
		file := hardeningFile(9001)
		id, err := ts.repo.Create(txCtx, file)
		if err != nil {
			return model.File{}, err
		}
		file.ID = id
		_, err = ts.db.DB().Exec(txCtx, "save_probe_job", "insert into probe_jobs (file_id) values ($1)", id)
		return file, err
	}
	const workers = 12
	results := make(chan model.File, workers)
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			file, err := ts.repo.FinalizeUpload(ctx, key, binding, false, create)
			results <- file
			failures <- err
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var fileID int64
	for file := range results {
		if fileID == 0 {
			fileID = file.ID
		}
		require.Equal(t, fileID, file.ID)
	}
	require.EqualValues(t, 1, calls.Load())
	var jobs int
	require.NoError(t, ts.db.DB().QueryRow(ctx, "count_probe_jobs", "select count(*) from probe_jobs").Scan(&jobs))
	require.Equal(t, 1, jobs)
	_, err = ts.repo.FinalizeUpload(ctx, key, strings.Repeat("b", 64), false, create)
	require.ErrorIs(t, err, model.ErrFinalizationConflict)
	require.NoError(t, ts.repo.DeleteByID(ctx, fileID))
	_, err = ts.repo.FinalizeUpload(ctx, key, binding, false, create)
	require.ErrorIs(t, err, model.ErrFinalizationGone)
	require.EqualValues(t, 1, calls.Load())
	failure := errors.New("fail before commit")
	_, err = ts.repo.FinalizeUpload(ctx, uuid.NewString(), binding, false, func(txCtx context.Context) (model.File, error) {
		file, err := create(txCtx)
		if err != nil {
			return file, err
		}
		return file, failure
	})
	require.ErrorIs(t, err, failure)
	require.NoError(t, ts.db.DB().QueryRow(ctx, "count_probe_jobs", "select count(*) from probe_jobs").Scan(&jobs))
	require.Equal(t, 1, jobs, "file creation and job must roll back together")
}

func TestIntegrationHardeningFileTypePositionAndPrimary(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	first := hardeningFile(9002)
	first.ID = mustCreateHardeningFile(t, ctx, ts, first)
	second := hardeningFile(9002)
	second.ID = mustCreateHardeningFile(t, ctx, ts, second)
	read, err := ts.repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, model.FileTypeImage, read.FileType)
	read.FileType = model.FileTypeVideo
	read.MimeType = "video/mp4"
	require.NoError(t, ts.repo.Update(ctx, read.ID, read))
	read, err = ts.repo.GetByID(ctx, read.ID)
	require.NoError(t, err)
	require.Equal(t, model.FileTypeVideo, read.FileType)
	images, _, err := ts.repo.List(ctx, filerepo.ListFilters{
		ObjectType: "exercise",
		ObjectID:   9002,
		FileType:   "image",
		Limit:      1000,
		SkipTotal:  true,
	})
	require.NoError(t, err)
	require.Len(t, images, 1)
	require.Equal(t, second.ID, images[0].ID)
	require.NoError(t, ts.repo.UpdatePosition(ctx, first.ID, 7))
	read, err = ts.repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, 7, read.Position)
	failures := make(chan error, 2)
	for _, id := range []int64{first.ID, second.ID} {
		go func(id int64) {
			failures <- ts.trx.RunInTx(ctx, func(txCtx context.Context) error {
				if err := ts.repo.ClearPrimary(txCtx, "exercise", 9002, id); err != nil {
					return err
				}
				return ts.repo.SetPrimary(txCtx, id, "exercise", 9002)
			})
		}(id)
	}
	require.NoError(t, <-failures)
	require.NoError(t, <-failures)
	var count int
	row := ts.db.DB().QueryRow(ctx, "count_primary",
		"select count(*) from files where object_id=9002 and is_primary and deleted_at is null",
	)
	require.NoError(t, row.Scan(&count))
	require.Equal(t, 1, count)
	require.ErrorIs(t, ts.repo.SetPrimary(ctx, first.ID, "exercise", 9999), model.ErrObjectBindingMismatch)
	read, err = ts.repo.GetByID(ctx, first.ID)
	require.NoError(t, err)
	require.EqualValues(t, 9002, *read.ObjectID)
}

func mustCreateHardeningFile(t *testing.T, ctx context.Context, ts *TestRepoSuite, file model.File) int64 {
	t.Helper()
	id, err := ts.repo.Create(ctx, file)
	require.NoError(t, err)
	return id
}

func TestIntegrationHardeningPendingDeletionSurvivesCleaner(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer func() {
		_, _ = ts.db.DB().Exec(ctx, "remove_test_plans", "delete from file_deletions")
		ts.cleanUp(ctx)
	}()
	file := hardeningFile(9003)
	file.ID = mustCreateHardeningFile(t, ctx, ts, file)
	require.NoError(t, ts.trx.RunInTx(ctx, func(txCtx context.Context) error {
		if _, err := ts.repo.GetByIDForUpdate(txCtx, file.ID); err != nil {
			return err
		}
		if err := ts.repo.SaveDeletionPlan(txCtx, model.DeletionPlan{
			File:   file,
			Paths:  []string{"media/v1/file/main.png"},
			UserID: identity.NewUserID(),
		}); err != nil {
			return err
		}
		return ts.repo.DeleteByID(txCtx, file.ID)
	}))
	_, err := ts.db.DB().Exec(ctx, "age_deleted_file", "update files set deleted_at=now()-interval '2 hours' where id=$1", file.ID)
	require.NoError(t, err)
	removed, err := ts.repo.CleanupExpiredFiles(ctx, 100, 60)
	require.NoError(t, err)
	require.Zero(t, removed)
	require.NoError(t, ts.trx.RunInTx(ctx, func(txCtx context.Context) error {
		plan, found, err := ts.repo.GetDeletionPlanForUpdate(txCtx, file.ID)
		if err != nil {
			return err
		}
		if !found || plan.Completed {
			return errors.New("pending plan was lost")
		}
		return ts.repo.CompleteDeletionPlan(txCtx, file.ID)
	}))
	removed, err = ts.repo.CleanupExpiredFiles(ctx, 100, 60)
	require.NoError(t, err)
	require.EqualValues(t, 1, removed)
}
