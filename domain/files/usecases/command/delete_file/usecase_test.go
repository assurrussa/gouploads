package deletefile_test

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testsmatcher "github.com/assurrussa/gouploads/domain/files/tests/matcher"
	deletedfile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	deletedfilemocks "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file/mocks"
	eventstreammocks "github.com/assurrussa/gouploads/internal/events/mocks"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/internal/pointer"
	tests "github.com/assurrussa/gouploads/internal/testsupport"
)

type TestSuite struct {
	suite.Suite

	fileMock        *deletedfilemocks.MockfileRepository
	transactorMock  *deletedfilemocks.Mocktransactor
	storageMock     *deletedfilemocks.MockfileStorage
	outboxMock      *deletedfilemocks.MockoutboxPutter
	eventStreamMock *eventstreammocks.MockPublisher

	useCase   *deletedfile.UseCase
	errExpect error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		transactorMock := deletedfilemocks.NewMocktransactor(ctrl)
		fileMock := deletedfilemocks.NewMockfileRepository(ctrl)
		outboxMock := deletedfilemocks.NewMockoutboxPutter(ctrl)
		storageMock := deletedfilemocks.NewMockfileStorage(ctrl)
		eventStreamMock := eventstreammocks.NewMockPublisher(ctrl)

		useCase := deletedfile.Must(deletedfile.NewOptions(
			transactorMock,
			fileMock,
			eventStreamMock,
			log,
			storageMock,
			outboxMock,
		))

		return &TestSuite{
			useCase:         useCase,
			fileMock:        fileMock,
			transactorMock:  transactorMock,
			eventStreamMock: eventStreamMock,
			outboxMock:      outboxMock,
			storageMock:     storageMock,
			errExpect:       errors.New("expected error"),
		}
	})
}

func TestHandle_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		deletedfile.Must(deletedfile.NewOptions(nil, nil, nil, nil, nil, nil))
	})
}

func TestHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:               fileID,
		FileName:         "testname.png",
		OriginalFileName: "testname-original.png",
		FolderPath:       "path/foo",
		ObjectID:         pointer.To(shared.FileObjectID(fileID)),
		ObjectType:       shared.ObjectTypeAdmin,
		URL:              "https://example.com/path/foo/testname.png",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, nil).Times(1)
	ts.fileMock.EXPECT().DeleteByID(ctx, fileID).Return(nil).Times(1)
	ts.storageMock.EXPECT().Delete(ctx, file.GetFullPath()).Return(nil).Times(1)
	eventFileUpload := shared.NewFileDeletedEvent(fileID, file.GetPublicURL(), shared.FileDeleteStatusCompleted)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, testsmatcher.NewEventPublishDeletedMatcher(
			"file publish success matcher", eventFileUpload,
		)).
		Return(nil).Times(1)

	payloadBytes := `{"userId":"368c1d49-713e-4b7f-9e8c-bd2b73b1d274","userType":"admin","fileId":123,"eventType":"model_deleted_bind","meta":{"fileName":"testname.png","fileUrl":"https://example.com/path/foo/testname.png","foo":"bar","objectId":123,"objectType":"admin","originalFileName":"testname-original.png"}}` //nolint:lll // tests
	ts.outboxMock.EXPECT().Put(gomock.Any(), "model_deleted_bind", payloadBytes, gomock.Any()).
		DoAndReturn(func(_ context.Context, name, payload string, availableAt time.Time) (int64, error) {
			var dataPayload map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &dataPayload))
			ts.Equal("model_deleted_bind", name)
			ts.NotZero(availableAt)
			ts.InDelta(float64(fileID), dataPayload["fileId"], 0.1)
			meta, ok := dataPayload["meta"].(map[string]any)
			ts.True(ok)
			ts.Equal("bar", meta["foo"])
			return 777, nil
		}).Times(1)

	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_RemoveStorageFileWithPresets(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:               fileID,
		FileName:         "main.png",
		OriginalFileName: "testname-original.png",
		FolderPath:       "media/v1/admin/123/file-slug",
		ObjectID:         pointer.To(shared.FileObjectID(fileID)),
		ObjectType:       shared.ObjectTypeAdmin,
		Data: &model.FileData{
			Presets: map[shared.PresetName]shared.FilePreset{
				shared.FilePresetMainName: {
					PresetName:   shared.FilePresetMainName.String(),
					RelativePath: "media/v1/admin/123/file-slug/main.png",
				},
				"thumb": {
					PresetName:   "thumb",
					RelativePath: "media/v1/admin/123/file-slug/thumb.png",
				},
				"original": {
					PresetName:   "original",
					RelativePath: "media/v1/admin/123/file-slug/original.png",
				},
			},
		},
		URL: "https://example.com/media/v1/admin/123/file-slug/main.png",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, nil).Times(1)
	ts.fileMock.EXPECT().DeleteByID(ctx, fileID).Return(nil).Times(1)
	ts.storageMock.EXPECT().DeleteBatch(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, paths []string) error {
			ts.ElementsMatch([]string{
				file.GetFullPath(),
				path.Join(file.FolderPath, "thumb", file.FileName),
				"media/v1/admin/123/file-slug/thumb.png",
				path.Join(file.FolderPath, "original", file.FileName),
				"media/v1/admin/123/file-slug/original.png",
			}, paths)
			return nil
		}).Times(1)
	eventFileUpload := shared.NewFileDeletedEvent(fileID, file.GetPublicURL(), shared.FileDeleteStatusCompleted)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, testsmatcher.NewEventPublishDeletedMatcher(
			"file publish success matcher", eventFileUpload,
		)).
		Return(nil).Times(1)

	payloadBytes := `{"userId":"368c1d49-713e-4b7f-9e8c-bd2b73b1d274","userType":"admin","fileId":123,"eventType":"model_deleted_bind","meta":{"fileName":"main.png","fileUrl":"https://example.com/media/v1/admin/123/file-slug/main.png","foo":"bar","objectId":123,"objectType":"admin","originalFileName":"testname-original.png"}}` //nolint:lll // tests
	ts.outboxMock.EXPECT().Put(gomock.Any(), "model_deleted_bind", payloadBytes, gomock.Any()).
		DoAndReturn(func(_ context.Context, name, payload string, availableAt time.Time) (int64, error) {
			var dataPayload map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &dataPayload))
			ts.Equal("model_deleted_bind", name)
			ts.NotZero(availableAt)
			return 777, nil
		}).Times(1)

	resp, err := ts.useCase.Handle(ctx, req)

	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_RejectsFileOwnedByDifferentObject(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})

	const fileID = int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "main.png",
		FolderPath: "media/v1/admin/9/file-slug",
		ObjectType: shared.ObjectTypeAdmin,
		ObjectID:   pointer.To(shared.FileObjectID(9)),
	}
	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, nil).Times(1)

	_, err := ts.useCase.Handle(ctx, deletedfile.Request{
		FileID:     fileID,
		ObjectType: shared.ObjectTypeAdmin,
		ObjectID:   shared.FileObjectID(3),
	})
	ts.Require().ErrorIs(err, deletedfile.ErrFileOwnershipMismatch)
}

func TestHandle_RemoveStorageFile(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, nil).Times(1)
	ts.fileMock.EXPECT().DeleteByID(ctx, fileID).Return(nil).Times(1)
	ts.storageMock.EXPECT().Delete(ctx, file.GetFullPath()).Return(ts.errExpect).Times(1)
	eventFileUpload := shared.NewFileDeletedEvent(fileID, file.GetPublicURL(), shared.FileDeleteStatusFailed)
	eventFileUpload.Error = ts.errExpect.Error()
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, testsmatcher.NewEventPublishDeletedMatcher(
			"file publish error matcher", eventFileUpload,
		)).
		Return(nil).Times(1)

	payloadBytes := `{"userId":"368c1d49-713e-4b7f-9e8c-bd2b73b1d274","userType":"admin","fileId":123,"eventType":"model_deleted_bind","meta":{"fileName":"testname.png","foo":"bar"}}` //nolint:lll // tests
	ts.outboxMock.EXPECT().Put(gomock.Any(), "model_deleted_bind", payloadBytes, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, payload string, _ time.Time) (int64, error) {
			var dataPayload map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &dataPayload))
			return 777, nil
		}).Times(1)

	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().ErrorIs(err, ts.errExpect)
	ts.Empty(resp)
}

func TestHandle_FailedEnqueueAfterJobs(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, nil).Times(1)
	ts.fileMock.EXPECT().DeleteByID(ctx, fileID).Return(nil).Times(1)
	eventFileUpload := shared.NewFileDeletedEvent(fileID, file.GetPublicURL(), shared.FileDeleteStatusFailed)
	eventFileUpload.Error = ts.errExpect.Error()
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, testsmatcher.NewEventPublishDeletedMatcher(
			"file publish error matcher", eventFileUpload,
		)).
		Return(nil).Times(1)

	payloadBytes := `{"userId":"368c1d49-713e-4b7f-9e8c-bd2b73b1d274","userType":"admin","fileId":123,"eventType":"model_deleted_bind","meta":{"fileName":"testname.png","foo":"bar"}}` //nolint:lll // tests
	ts.outboxMock.EXPECT().Put(gomock.Any(), "model_deleted_bind", payloadBytes, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, payload string, _ time.Time) (int64, error) {
			var dataPayload map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &dataPayload))
			return 777, ts.errExpect
		}).Times(1)

	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().ErrorIs(err, ts.errExpect)
	ts.Empty(resp)
}

func TestHandle_FailedDeleteByID(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, nil).Times(1)
	ts.fileMock.EXPECT().DeleteByID(ctx, fileID).Return(ts.errExpect).Times(1)
	eventFileUpload := shared.NewFileDeletedEvent(fileID, file.GetPublicURL(), shared.FileDeleteStatusFailed)
	eventFileUpload.Error = ts.errExpect.Error()
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, testsmatcher.NewEventPublishDeletedMatcher(
			"file publish error matcher", eventFileUpload,
		)).
		Return(nil).Times(1)

	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().ErrorIs(err, ts.errExpect)
	ts.Empty(resp)
}

func TestHandle_FileIDIsEmpty_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(0)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.storageMock.EXPECT().Delete(ctx, file.GetFullPath()).Return(nil).Times(1)
	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_FileIDIsEmpty_Error(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(0)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}
	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	req := deletedfile.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}

	ts.storageMock.EXPECT().Delete(ctx, file.GetFullPath()).Return(ts.errExpect).Times(1)
	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().ErrorIs(err, ts.errExpect)
	ts.Empty(resp)
}

func TestHandle_GetFileID_Error(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}
	req := deletedfile.Request{
		UserID:   userID,
		FileID:   fileID,
		FilePath: file.GetFullPath(),
	}

	ts.fileMock.EXPECT().GetByIDForUpdate(ctx, fileID).Return(file, ts.errExpect).Times(1)

	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().ErrorIs(err, ts.errExpect)
	ts.Empty(resp)
}

func TestHandle_FilePath_Empty(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(0)
	req := deletedfile.Request{
		UserID: userID,
		FileID: fileID,
	}

	// Action.
	resp, err := ts.useCase.Handle(ctx, req)

	// Assertion.
	ts.Require().Error(err)
	ts.Empty(resp)
}
