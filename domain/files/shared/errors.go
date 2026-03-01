package shared

import "errors"

var (
	ErrUnknownUploadUser = errors.New("unknown upload user")
	ErrInvalidParameters = errors.New("invalid parameters")

	ErrFileHeaderIsNil = errors.New("file header is nil")
)
