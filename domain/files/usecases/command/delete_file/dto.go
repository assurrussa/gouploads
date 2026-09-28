package deletefile

import (
	"errors"
	"fmt"

	"github.com/assurrussa/gouploads/domain/files/shared"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
	validator "github.com/assurrussa/gouploads/internal/validation"
)

type Request struct {
	FilePath    string
	UserID      sharedtypes.UserID
	FileID      int64
	ObjectType  shared.FileObjectType
	ObjectID    shared.FileObjectID
	AfterEvents []shared.FileEventAfterJob
}

func (r Request) Validate() error {
	if r.FileID < 1 && r.FilePath == "" {
		return errors.New("invalid file id and file path")
	}
	if r.ObjectType == "" && r.ObjectID == 0 {
		return validator.Validator.Struct(r)
	}
	if err := r.ObjectType.Validate(); err != nil {
		return fmt.Errorf("validate object type: %w", err)
	}
	if r.ObjectID <= 0 {
		return errors.New("invalid object id")
	}

	return validator.Validator.Struct(r)
}

type Response struct{}
