package model

import "errors"

var (
	ErrFileNotFound          = errors.New("file not found")
	ErrObjectBindingMismatch = errors.New("file does not belong to the requested object")
	ErrFinalizationConflict  = errors.New("finalization key belongs to another upload binding")
	ErrFinalizationGone      = errors.New("finalized upload has been deleted")
)
