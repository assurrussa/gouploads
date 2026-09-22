package fileloader_test

import (
	"context"
	"errors"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	commonmodel "github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/fileloader"
	fileloadermocks "github.com/assurrussa/gouploads/domain/files/service/fileloader/mocks"
)

type TestSuite struct {
	suite.Suite

	ctrl         *gomock.Controller
	mockFileRepo *fileloadermocks.MockFileRepo

	srv *fileloader.Service
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockFileRepo := fileloadermocks.NewMockFileRepo(ctrl)
		srv := fileloader.Must(fileloader.NewOptions(mockFileRepo, logger.Discard()))

		return &TestSuite{
			ctrl:         ctrl,
			mockFileRepo: mockFileRepo,
			srv:          srv,
		}
	})
}

func Test_LoadPreviewIDInvalid(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file, err := ts.srv.LoadPreview(ctx, -1)
	ts.Require().Error(err)
	ts.Nil(file)

	file, err = ts.srv.LoadPreview(ctx, 0)
	ts.Require().NoError(err)
	ts.Nil(file)
}

func Test_LoadPreview_ModelFileIDInvalid(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	previewFileID := int64(0)
	file, err := ts.srv.LoadPreview(ctx, previewFileID)
	ts.Require().NoError(err)
	ts.Nil(file)

	previewFileID = int64(-1)
	file, err = ts.srv.LoadPreview(ctx, previewFileID)
	ts.Require().Error(err)
	ts.Nil(file)
}

func Test_LoadPreview_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	previewFileID := int64(1234)
	file := commonmodel.File{
		ID:         previewFileID,
		FolderPath: "/folder/path",
		FileName:   "filename.png",
	}

	ts.mockFileRepo.EXPECT().GetByID(ctx, previewFileID).Return(file, nil).Times(1)

	fileRes, err := ts.srv.LoadPreview(ctx, previewFileID)
	ts.Require().NoError(err)
	ts.Equal(file, *fileRes)
}

func Test_LoadPreview_ErrorGetByID(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	errExpect := errors.New("expected error")

	previewFileID := int64(1234)
	file := commonmodel.File{
		ID: previewFileID,
	}

	ts.mockFileRepo.EXPECT().GetByID(ctx, previewFileID).Return(file, errExpect).Times(1)

	fileRes, err := ts.srv.LoadPreview(ctx, previewFileID)
	ts.Require().ErrorIs(err, errExpect)
	ts.Nil(fileRes)
}

func Test_LoadPreview_ErrorGetByIDEmptyFile(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	previewFileID := int64(1234)
	file := commonmodel.File{}

	ts.mockFileRepo.EXPECT().GetByID(ctx, previewFileID).Return(file, nil).Times(1)

	fileRes, err := ts.srv.LoadPreview(ctx, previewFileID)
	ts.Require().Error(err)
	ts.Nil(fileRes)
}

func Test_LoadPreviewURL_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	previewFileID := int64(1234)
	file := commonmodel.File{
		ID:         previewFileID,
		FolderPath: "/folder/path/",
		FileName:   "filename.png",
	}

	ts.mockFileRepo.EXPECT().GetByID(ctx, previewFileID).Return(file, nil).Times(1)

	fileResURL, err := ts.srv.LoadPreviewURL(ctx, previewFileID)
	ts.Require().NoError(err)
	ts.Equal("/folder/path/filename.png", fileResURL)
}

func Test_LoadPreviewURL_SuccessWithURL(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	previewFileID := int64(1234)
	file := commonmodel.File{
		ID:         previewFileID,
		URL:        "https://url.com/folder/path/filename.png",
		FolderPath: "/folder/path/",
		FileName:   "filename.png",
	}

	ts.mockFileRepo.EXPECT().GetByID(ctx, previewFileID).Return(file, nil).Times(1)

	fileResURL, err := ts.srv.LoadPreviewURL(ctx, previewFileID)
	ts.Require().NoError(err)
	ts.Equal("https://url.com/folder/path/filename.png", fileResURL)
}

func Test_LoadPreviewURL_Error(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	previewFileID := int64(1234)
	errExpect := errors.New("expected error")
	file := commonmodel.File{
		ID:         previewFileID,
		FolderPath: "folder/path",
		FileName:   "filename.png",
	}

	ts.mockFileRepo.EXPECT().GetByID(ctx, previewFileID).Return(file, errExpect).Times(1)

	fileResURL, err := ts.srv.LoadPreviewURL(ctx, previewFileID)
	ts.Require().ErrorIs(err, errExpect)
	ts.Empty(fileResURL)
}
