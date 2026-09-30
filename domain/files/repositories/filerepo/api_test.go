//go:build integration

package filerepo_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	querybuilder "github.com/assurrussa/outbox/shared/query_builder"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	"github.com/assurrussa/gouploads/internal/pointer"
	tests "github.com/assurrussa/gouploads/internal/testsupport"
)

type TestRepoSuite struct {
	suite.Suite
	db       pgsql.Client
	dbHelper *testshelpers.DBHelper
	trx      *transaction.Manager
	cleanUp  func(context.Context)
	repo     *filerepo.Repo
}

func NewTestRepoSuite(t *testing.T, opts ...testshelpers.OptionDatabase) (context.Context, context.CancelFunc, *TestRepoSuite) {
	t.Helper()
	return tests.NewSuite[*TestRepoSuite](t, func(t *testing.T, ctx context.Context) *TestRepoSuite {
		t.Helper()
		db, helper, cleanup := testshelpers.PrepareDB(ctx, t, "TestFilesRepoSuite", opts...)
		tx := transaction.New(db.DB())
		return &TestRepoSuite{db: db, dbHelper: helper, trx: tx, cleanUp: cleanup, repo: filerepo.Must(filerepo.NewOptions(db, tx))}
	})
}

func TestIntegration_Init(t *testing.T) {
	require.Panics(t, func() { filerepo.Must(filerepo.NewOptions(nil, nil)) })
}

func TestIntegration_Create(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	file := createModel("TestName", "test_slug", time.Now().UTC())
	id, err := ts.repo.Create(ctx, file)
	require.NoError(t, err)
	require.Positive(t, id)
	id, err = ts.repo.Create(ctx, file)
	require.ErrorIs(t, err, pgsql.ErrRowAlreadyExists)
	require.Zero(t, id)
}

func TestIntegration_GetByID(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	files := createModels(t, ctx, ts, 5)
	file, err := ts.repo.GetByID(ctx, files[0].ID)
	require.NoError(t, err)
	require.Equal(t, files[0].Name, file.Name)
	file, err = ts.repo.GetByID(ctx, 99999)
	require.NoError(t, err)
	require.Zero(t, file.ID)
	_, err = ts.repo.GetByID(ctx, 0)
	require.Error(t, err)
}

func TestIntegration_DeleteByID(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	files := createModels(t, ctx, ts, 2)
	require.NoError(t, ts.repo.DeleteByID(ctx, files[0].ID))
	file, err := ts.repo.GetByID(ctx, files[0].ID)
	require.NoError(t, err)
	require.Zero(t, file.ID)
	require.NoError(t, ts.repo.DeleteByID(ctx, files[0].ID))
	require.NoError(t, ts.repo.DeleteByID(ctx, 99999))
	require.Error(t, ts.repo.DeleteByID(ctx, 0))
}

func TestIntegration_Update(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	files := createModels(t, ctx, ts, 1)
	file := files[0]
	slug := file.Slug
	created := file.CreatedAt
	file.Name = "updated"
	file.Description = pointer.To("description")
	file.Slug = "not mutable"
	file.CreatedAt = time.Now().UTC()
	require.NoError(t, ts.repo.Update(ctx, file.ID, file))
	got, err := ts.repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, slug, got.Slug)
	require.Equal(t, file.Name, got.Name)
	require.Equal(t, file.Description, got.Description)
	require.True(t, created.Equal(got.CreatedAt))
	require.NoError(t, ts.repo.DeleteByID(ctx, file.ID))
	require.ErrorIs(t, ts.repo.Update(ctx, file.ID, file), model.ErrFileNotFound)
}

func TestRepo_PrimaryControls(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	objectType := shared.ObjectTypeExercise
	objectID := shared.FileObjectID(9101)
	stamp := time.Now().UTC().Truncate(time.Second)
	primary := createObjectFile("Primary", "primary", stamp, objectType, objectID, true)
	primaryID, err := ts.repo.Create(ctx, primary)
	require.NoError(t, err)
	secondary := createObjectFile("Secondary", "secondary", stamp.Add(time.Minute), objectType, objectID, false)
	secondaryID, err := ts.repo.Create(ctx, secondary)
	require.NoError(t, err)
	foreign := createObjectFile("Foreign", "foreign", stamp, shared.ObjectTypeAdmin, 55, false)
	foreignID, err := ts.repo.Create(ctx, foreign)
	require.NoError(t, err)
	require.ErrorIs(t, ts.repo.SetPrimary(ctx, foreignID, objectType.String(), objectID.Int64()), model.ErrObjectBindingMismatch)
	require.NoError(t, ts.trx.RunInTx(ctx, func(ctx context.Context) error {
		if err := ts.repo.ClearPrimary(ctx, objectType.String(), objectID.Int64(), 0); err != nil {
			return err
		}
		return ts.repo.SetPrimary(ctx, secondaryID, objectType.String(), objectID.Int64())
	}))
	got, err := ts.repo.GetByID(ctx, primaryID)
	require.NoError(t, err)
	require.False(t, got.IsPrimary)
	got, err = ts.repo.GetByID(ctx, secondaryID)
	require.NoError(t, err)
	require.True(t, got.IsPrimary)
	require.Equal(t, objectID, *got.ObjectID)
	require.NoError(t, ts.repo.ClearPrimary(ctx, objectType.String(), objectID.Int64(), secondaryID))
	files, err := ts.repo.GetByObjectType(ctx, objectType.String(), objectID.Int64())
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, secondaryID, files[0].ID)
}

func TestIntegration_CleanupExpiredFiles(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	_ = createModels(t, ctx, ts, 100)
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	id, err := ts.repo.Create(ctx, createModel("extra", "extra_slug", stamp))
	require.NoError(t, err)
	query := querybuilder.BuilderDollar().Update("files").Set("deleted_at", sql.NullTime{
		Time:  stamp,
		Valid: true,
	}).Where(squirrel.Or{
		squirrel.Expr("id % 3 = 0"),
		squirrel.Eq{"id": id},
	})
	_, err = ts.db.DB().Execx(ctx, "mark_deleted", query)
	require.NoError(t, err)
	countRows := func() int {
		var count int
		require.NoError(t, ts.db.DB().QueryRow(ctx, "count_files", "select count(*) from files").Scan(&count))
		return count
	}
	require.Equal(t, 101, countRows())
	tx, err := ts.db.DB().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "select id from files where id=$1 for update", id)
	require.NoError(t, err)
	count, err := ts.repo.CleanupExpiredFiles(ctx, 3, 60)
	require.NoError(t, err)
	require.EqualValues(t, 3, count)
	count, err = ts.repo.CleanupExpiredFiles(ctx, 1000, 60)
	require.NoError(t, err)
	require.EqualValues(t, 30, count)
	require.Equal(t, 68, countRows())
	require.NoError(t, tx.Commit(ctx))
	count, err = ts.repo.CleanupExpiredFiles(ctx, 1000, 60)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Equal(t, 67, countRows())
}

func createModels(t *testing.T, ctx context.Context, ts *TestRepoSuite, size int) []model.File {
	t.Helper()
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	files := make([]model.File, 0, size)
	for i := 1; i <= size; i++ {
		suffix := "__" + strconv.Itoa(i)
		file := model.File{
			Name:        "Name" + suffix,
			Description: pointer.To("Description" + suffix),
			Slug:        "slug" + suffix,
			URL:         "url" + suffix,
			Locale:      pointer.To("EN_en"),
			CreatedAt:   stamp.Add(time.Duration(i) * time.Minute),
			UpdatedAt:   stamp.Add(time.Duration(i) * time.Minute),
			PublishedAt: sql.NullTime{
				Valid: i%3 != 0,
				Time:  stamp,
			},
		}
		id, err := ts.repo.Create(ctx, file)
		require.NoError(t, err)
		file.ID = id
		files = append(files, file)
	}
	return files
}

func createModel(name, slug string, stamp time.Time) model.File {
	return model.File{Name: name, Description: pointer.To(""), Slug: slug, CreatedAt: stamp, UpdatedAt: stamp}
}

func createObjectFile(
	name, slug string, stamp time.Time, objectType shared.FileObjectType, objectID shared.FileObjectID, primary bool,
) model.File {
	file := createModel(name, slug, stamp)
	file.ObjectType = objectType
	file.ObjectID = pointer.To(objectID)
	file.FileName = slug + ".png"
	file.OriginalFileName = file.FileName
	file.FolderPath = fmt.Sprintf("uploads/%s/%d", objectType, objectID)
	file.URL = "/" + file.FolderPath + "/" + file.FileName
	file.MimeType = "image/png"
	file.FileType = model.FileTypeImage
	file.Size = 1024
	file.IsPrimary = primary
	return file
}
