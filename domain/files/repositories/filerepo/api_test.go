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
	"github.com/assurrussa/goshared/pkg/pointer"
	querybuilder "github.com/assurrussa/outbox/shared/query_builder"
	tests "github.com/assurrussa/goshared/pkg/tests"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
)

type TestRepoSuite struct {
	suite.Suite

	db       pgsql.Client
	dbHelper *testshelpers.DBHelper
	trx      *transaction.Manager
	cleanUp  func(context.Context)

	repo *filerepo.Repo
}

func NewTestRepoSuite(t *testing.T, opts ...testshelpers.OptionDatabase) (context.Context, context.CancelFunc, *TestRepoSuite) {
	return tests.NewSuite[*TestRepoSuite](t, func(t *testing.T, ctx context.Context) *TestRepoSuite {
		db, dbHelper, cleanUp := testshelpers.PrepareDB(ctx, t, "TestFilesRepoSuite", opts...)
		trx := transaction.New(db.DB())
		repo := filerepo.Must(filerepo.NewOptions(db, trx))

		return &TestRepoSuite{
			db:       db,
			dbHelper: dbHelper,
			trx:      trx,
			cleanUp:  cleanUp,
			repo:     repo,
		}
	})
}

func TestIntegration_Init(t *testing.T) {
	assert.Panics(t, func() {
		filerepo.Must(filerepo.NewOptions(nil, nil))
	})
}

func TestIntegration_Create(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)

	tmCreate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	ts.Run("rowModel does not exist, should be created", func() {
		rowModel := createModel("TestName", "test_slug", tmCreate)

		rowModelID, err := ts.repo.Create(ctx, rowModel)
		ts.Require().NoError(err)
		ts.NotEmpty(rowModelID)
	})

	ts.Run("rowModel already exists", func() {
		rowModel := createModel("TestName2", "test_slug2", tmCreate)

		rowModelID, err := ts.repo.Create(ctx, rowModel)
		ts.Require().NoError(err)
		ts.NotEmpty(rowModelID)

		// check unique duplicate
		rowModelID, err = ts.repo.Create(ctx, rowModel)
		ts.Require().Error(err)
		ts.Require().ErrorIs(err, pgsql.ErrRowAlreadyExists)
		ts.Empty(rowModelID)
	})
}

func TestIntegration_GetByID(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)

	data := createModels(t, ts, ctx, 5)
	rowModelID := data[0].ID

	ts.Run("rowModel exist", func() {
		acc, err := ts.repo.GetByID(ctx, rowModelID)
		ts.Require().NoError(err)
		ts.NotEmpty(acc)
	})

	ts.Run("rowModel does not exist", func() {
		acc, err := ts.repo.GetByID(ctx, 99999)
		ts.Require().NoError(err)
		ts.Empty(acc)
	})

	ts.Run("rowModel does not exist zero", func() {
		acc, err := ts.repo.GetByID(ctx, 0)
		ts.Require().Error(err)
		ts.Empty(acc)
	})
}

func TestIntegration_DeleteByID(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)

	data := createModels(t, ts, ctx, 5)
	rowModelID := data[0].ID

	ts.Run("rowModel exist", func() {
		acc, err := ts.repo.GetByID(ctx, rowModelID)
		ts.Require().NoError(err)
		ts.NotEmpty(acc)
		err = ts.repo.DeleteByID(ctx, rowModelID)
		ts.Require().NoError(err)
		acc, err = ts.repo.GetByID(ctx, rowModelID)
		ts.Require().NoError(err)
		ts.Empty(acc)
	})

	ts.Run("rowModel does not exist", func() {
		err := ts.repo.DeleteByID(ctx, 99999)
		ts.Require().NoError(err)
	})

	ts.Run("rowModel does not exist zero", func() {
		err := ts.repo.DeleteByID(ctx, 0)
		ts.Require().Error(err)
	})
}

func TestIntegration_Update(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)

	data := createModels(t, ts, ctx, 5)
	rowModel := data[0]

	ts.Run("rowModel update", func() {
		acc, err := ts.repo.GetByID(ctx, rowModel.ID)
		ts.Require().NoError(err)
		ts.NotEmpty(acc)
		ts.Equal(rowModel.Name, acc.Name)
		ts.Equal(rowModel.Slug, acc.Slug)
		ts.Equal(rowModel.Description, acc.Description)
		rowModel.Name = "testNameUpdate"
		actualSlug := rowModel.Slug
		rowModel.Slug = "testSlugUpdate"
		rowModel.Description = pointer.To("testDescUpdate")
		err = ts.repo.Update(ctx, rowModel.ID, rowModel)
		ts.Require().NoError(err)
		acc, err = ts.repo.GetByID(ctx, rowModel.ID)
		ts.Require().NoError(err)
		ts.Equal(rowModel.Name, acc.Name)
		ts.Equal(actualSlug, acc.Slug)
		ts.Equal(rowModel.Description, acc.Description)
	})
}

func TestRepo_PrimaryControls(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)

	objectType := shared.ObjectTypeExercise
	baseTime := time.Now().UTC().Truncate(time.Second)

	ts.Run("clear and set primary keep exclusivity", func() {
		objectID := shared.FileObjectID(9101)

		primaryCandidate := createObjectFile(
			"Primary File",
			"primary-file-clear",
			baseTime,
			objectType,
			objectID,
			true,
		)

		primaryID, err := ts.repo.Create(ctx, primaryCandidate)
		ts.Require().NoError(err)
		primaryCandidate.ID = primaryID

		secondaryCandidate := createObjectFile(
			"Secondary File",
			"secondary-file-clear",
			baseTime.Add(time.Minute),
			shared.ObjectTypeAdmin,
			55,
			false,
		)

		secondaryID, err := ts.repo.Create(ctx, secondaryCandidate)
		ts.Require().NoError(err)
		secondaryCandidate.ID = secondaryID

		ts.Require().NoError(ts.repo.ClearPrimary(ctx, objectType.String(), objectID.Int64(), 0))

		updatedPrimary, err := ts.repo.GetByID(ctx, primaryID)
		ts.Require().NoError(err)
		ts.False(updatedPrimary.IsPrimary)

		ts.Require().NoError(ts.repo.SetPrimary(ctx, secondaryID, objectType.String(), objectID.Int64()))

		updatedSecondary, err := ts.repo.GetByID(ctx, secondaryID)
		ts.Require().NoError(err)
		ts.True(updatedSecondary.IsPrimary)
		ts.Equal(objectType, updatedSecondary.ObjectType)
		ts.Equal(objectID, pointer.Indirect(updatedSecondary.ObjectID))

		// exclude newly promoted primary from reset
		ts.Require().NoError(ts.repo.ClearPrimary(ctx, objectType.String(), objectID.Int64(), secondaryID))
		recheckedSecondary, err := ts.repo.GetByID(ctx, secondaryID)
		ts.Require().NoError(err)
		ts.True(recheckedSecondary.IsPrimary)

		files, err := ts.repo.GetByObjectType(ctx, objectType.String(), objectID.Int64())
		ts.Require().NoError(err)
		ts.Require().Len(files, 2)
		ts.Equal(secondaryID, files[0].ID)
		ts.True(files[0].IsPrimary)
	})
}

func TestIntegration_CleanupExpiredFiles(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	_ = createModels(t, ts, ctx, 100)
	tmCreateDel := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rowModel := createModel("TestName2", "test_slug2", tmCreateDel)
	rowModelID, err := ts.repo.Create(ctx, rowModel)
	require.NoError(t, err)
	sqlQueryDeleted := querybuilder.BuilderDollar().Update("files").
		Set("deleted_at", sql.NullTime{Time: tmCreateDel, Valid: true}).
		Where(squirrel.Or{
			squirrel.Expr("id % 3 = 0"),
			squirrel.Eq{"id": rowModelID},
		})
	_, err = ts.db.DB().Execx(ctx, "test", sqlQueryDeleted)
	require.NoError(t, err)

	fnCounts := func() int {
		var countRows int
		err := ts.db.DB().Getx(
			ctx,
			"count rows",
			&countRows,
			querybuilder.BuilderDollar().Select("count(id)").From("files").Limit(1),
		)
		ts.Require().NoError(err)

		return countRows
	}

	ts.Run("delete files", func() {
		countRows := fnCounts()

		const queryblock = `SELECT id FROM files WHERE id = $1 LIMIT 1 FOR UPDATE;`
		txManager, err := ts.db.DB().BeginTx(ctx, pgx.TxOptions{})
		ts.Require().NoError(err)
		_, err = txManager.Exec(ctx, queryblock, rowModelID)
		ts.Require().NoError(err)

		err = ts.trx.RunInTx(ctx, func(ctx context.Context) error {
			count, err := ts.repo.CleanupExpiredFiles(ctx, 3, 60)
			ts.Require().NoError(err)
			ts.Equal(int64(3), count)
			return nil
		})
		ts.Require().NoError(err)
		ts.Equal(countRows-3, fnCounts())

		count, err := ts.repo.CleanupExpiredFiles(ctx, 1000, 60)
		ts.Require().NoError(err)
		ts.Equal(int64(30), count)
		ts.Equal(countRows-33, fnCounts())

		ts.Require().NoError(txManager.Commit(ctx))

		err = ts.trx.RunInTx(ctx, func(ctx context.Context) error {
			count, err := ts.repo.CleanupExpiredFiles(ctx, 1000, 60)
			ts.Require().NoError(err)
			ts.Equal(int64(1), count)

			return nil
		})
		ts.Require().NoError(err)
		ts.Equal(67, fnCounts())
	})
}

func createModels(t *testing.T, ts *TestRepoSuite, ctx context.Context, size int) []model.File {
	t.Helper()

	tmCreate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	list := make([]model.File, 0, 100)
	for i := 1; i <= size; i++ {
		strIndex := "__" + strconv.Itoa(i)
		rowModel := model.File{
			Name:        "TestName_" + strIndex,
			Description: pointer.To("TestDesc_" + strIndex),
			Slug:        "TestSlug_" + strIndex,
			URL:         "TestUrl_" + strIndex,
			Locale:      pointer.To("EN_en"),
			CreatedAt:   tmCreate.Add(time.Duration(i) * time.Minute), // разные времена создания
			UpdatedAt:   tmCreate.Add(time.Duration(i) * time.Minute),
			PublishedAt: sql.NullTime{Valid: true, Time: tmCreate.Add(time.Duration(i) * time.Minute)},
		}

		if i%3 == 0 {
			rowModel.PublishedAt = sql.NullTime{}
		}

		id, err := ts.repo.Create(ctx, rowModel)
		ts.Require().NoError(err)
		rowModel.ID = id

		list = append(list, rowModel)
	}

	return list
}

func createModel(name string, slug string, tmCreate time.Time) model.File {
	return model.File{
		Name:        name,
		Description: pointer.To(""),
		Slug:        slug,
		URL:         "",
		CreatedAt:   tmCreate,
		UpdatedAt:   tmCreate,
	}
}

func createObjectFile(
	name string,
	slug string,
	tmCreate time.Time,
	objectType shared.FileObjectType,
	objectID shared.FileObjectID,
	isPrimary bool,
) model.File {
	file := createModel(name, slug, tmCreate)
	file.ObjectType = objectType
	file.ObjectID = pointer.To(objectID)
	file.FileName = fmt.Sprintf("%s.png", slug)
	file.OriginalFileName = fmt.Sprintf("%s.png", slug)
	file.FolderPath = fmt.Sprintf("uploads/%s/%d", objectType, objectID)
	file.URL = fmt.Sprintf("/%s/%s", file.FolderPath, file.FileName)
	file.MimeType = "image/png"
	file.Size = 1024
	file.IsPrimary = isPrimary

	return file
}
