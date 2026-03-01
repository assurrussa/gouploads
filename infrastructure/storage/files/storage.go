package filestorage

import (
	"context"
	"errors"
	"io"
)

// ErrNotSupported используется адаптерами, где операция не реализована.
var ErrNotSupported = errors.New("storage: operation not supported")

// Storage описывает абстракцию работы с файловым хранилищем (локальным или external).
type Storage interface {
	SavePersist(ctx context.Context, input SaveFileInput) (StoredFile, error)
	SaveTemp(ctx context.Context, input SaveFileInput) (StoredFile, error)
	Commit(ctx context.Context, input CommitInput) (StoredFile, error)
	Exists(ctx context.Context, input ExistFileInput) (ExistFile, error)
	Open(ctx context.Context, relativePath string) (io.ReadCloser, error)
	Delete(ctx context.Context, relativePath string) error
	DeleteBatch(ctx context.Context, relativePaths []string) error
}

// SaveFileInput описывает параметры сохранения временного файла.
type SaveFileInput struct {
	Dir      string
	FileName string
	Size     int64
	MimeType string
	Reader   io.Reader
}

func (in SaveFileInput) Validate() error {
	if in.Dir == "" {
		return errors.New("dir is required")
	}
	if in.FileName == "" {
		return errors.New("file name is required")
	}
	if in.Reader == nil {
		return errors.New("reader is required")
	}
	return nil
}

// CommitInput описывает перенос временного файла в постоянное хранилище.
type CommitInput struct {
	Path     string
	DestDir  string
	FileName string
}

func (in CommitInput) Validate() error {
	if in.Path == "" {
		return errors.New("path is required")
	}
	if in.DestDir == "" {
		return errors.New("dest dir is required")
	}
	if in.FileName == "" {
		return errors.New("file name is required")
	}
	return nil
}

// StoredFile описывает файл после переноса в постоянное хранилище.
type StoredFile struct {
	RelativePath string
	URL          string
	Size         int64
	MimeType     string
}

// ExistFileInput описывает существование файла в хранилище.
type ExistFileInput struct {
	Path string
}

func (in ExistFileInput) Validate() error {
	if in.Path == "" {
		return errors.New("temp path is required")
	}
	return nil
}

// ExistFile описывает существование файла в хранилище.
type ExistFile struct {
	Exist bool
}
