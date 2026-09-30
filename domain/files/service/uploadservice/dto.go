package uploadservice

import (
	"errors"
	"mime/multipart"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
	validator "github.com/assurrussa/gouploads/internal/validation"
)

var (
	ErrNoFiles      = errors.New("no files provided for upload")
	ErrTaskNotFound = errors.New("upload task not found")
	ErrFileNotFound = model.ErrFileNotFound
)

type BatchRequest struct {
	UploaderUUID sharedtypes.UserID `validate:"required"`
	ManagerID    int64
	UserID       int64
	FileHeaders  []*multipart.FileHeader `validate:"required"`
	ObjectType   shared.FileObjectType   `validate:"required"`
	ObjectID     shared.FileObjectID
	DeletedID    shared.FileObjectID
	AfterJobs    []shared.FileEventAfterJob
	Config       *FileUploadConfig
}

func (r BatchRequest) Validate() error {
	if r.UserID == 0 && r.ManagerID == 0 {
		return shared.ErrUnknownUploadUser
	}
	return validator.Validator.Struct(r)
}

type SingleRequest struct {
	UploaderUUID sharedtypes.UserID `validate:"required"`
	ManagerID    int64
	UserID       int64
	FileHeader   *multipart.FileHeader `validate:"required"`
	ObjectType   shared.FileObjectType `validate:"required"`
	ObjectID     shared.FileObjectID
	DeletedID    shared.FileObjectID
	AfterJobs    []shared.FileEventAfterJob
	Config       *FileUploadConfig
}

func (r SingleRequest) Validate() error {
	if r.UserID == 0 && r.ManagerID == 0 {
		return shared.ErrUnknownUploadUser
	}
	return validator.Validator.Struct(r)
}

type ReaderRequest struct {
	UploaderUUID sharedtypes.UserID `validate:"required"`
	ManagerID    int64
	UserID       int64
	ObjectType   shared.FileObjectType `validate:"required"`
	ObjectID     shared.FileObjectID
	DeletedID    shared.FileObjectID
	AfterJobs    []shared.FileEventAfterJob
	Config       *FileUploadConfig
	// FinalizationKey is the server-issued TUS completion UUID, never client metadata.
	// Empty keeps the ordinary non-TUS reader/stored upload contract.
	FinalizationKey string
}

func (r ReaderRequest) Validate() error {
	if r.UserID == 0 && r.ManagerID == 0 {
		return shared.ErrUnknownUploadUser
	}
	return validator.Validator.Struct(r)
}

type DeleteRequest struct {
	UserRequestID sharedtypes.UserID
	FileID        int64
	AfterJobs     []shared.FileEventAfterJob
}

type ValidationError struct{ Errors map[string]string }

func (e ValidationError) Error() string { return "upload validation error" }

type ClientError struct{ Message string }

func (e ClientError) Error() string { return e.Message }

// SetPrimaryRequest identifies a file already attached to the specified object.
type SetPrimaryRequest struct {
	FileID     int64
	ObjectType shared.FileObjectType
	ObjectID   int64
}

type File struct {
	ID             int64  `json:"id"`
	FileName       string `json:"fileName"`
	OriginalName   string `json:"originalName"`
	URL            string `json:"url"`
	Size           int64  `json:"size"`
	MimeType       string `json:"mimeType"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	IsPrimary      bool   `json:"isPrimary"`
	UploaderStatus string `json:"uploaderStatus"`
}

func ToFileTransform(file model.File, baseURL, bucket string) File {
	return File{
		ID: file.ID, FileName: file.FileName, OriginalName: file.OriginalFileName,
		URL: fileurl.Compose(baseURL, bucket, file.URL), Size: file.Size, MimeType: file.MimeType,
		IsPrimary:      file.IsPrimary,
		Width:          file.GetWidth(),
		Height:         file.GetHeight(),
		UploaderStatus: file.GetData().Uploader.Status.String(),
	}
}
