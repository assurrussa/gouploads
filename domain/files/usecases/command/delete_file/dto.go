package deletefile

import (
	"errors"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/validator"

	"github.com/assurrussa/gouploads/domain/files/shared"
)

type Request struct {
	FilePath    string
	UserID      sharedtypes.UserID
	FileID      int64
	AfterEvents []shared.FileEventAfterJob
}

func (r Request) Validate() error {
	if r.FileID < 1 && r.FilePath == "" {
		return errors.New("invalid file id and file path")
	}

	return validator.Validator.Struct(r)
}

type Response struct{}
