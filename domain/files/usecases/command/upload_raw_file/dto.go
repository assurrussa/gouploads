package uploadrawfile

import (
	"mime/multipart"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/validator"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

type Request struct {
	RequestID    sharedtypes.RequestID `validate:"required"`
	UploaderUUID sharedtypes.UserID    `validate:"required"`
	ManagerID    int64
	UserID       int64
	FileHeader   *multipart.FileHeader `validate:"required"`
	ObjectType   shared.FileObjectType `validate:"required"`
	ObjectID     shared.FileObjectID
	DeletedID    shared.FileObjectID
	AfterJobs    []shared.FileEventAfterJob
	Config       *uploadservice.FileUploadConfig
}

func (r Request) Validate() error {
	if r.UserID == 0 && r.ManagerID == 0 {
		return shared.ErrUnknownUploadUser
	}

	return validator.Validator.Struct(r)
}

type Response struct {
	File model.File
}
