package uploadservice

import (
	"context"
	"time"

	outboxtypes "github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

//go:generate toolsmocks

type transactor interface {
	RunInTx(ctx context.Context, f func(context.Context) error) error
}

type outboxPutter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}

// fileRepository defines required operations with file storage.
type fileRepository interface {
	Create(ctx context.Context, file model.File) (int64, error)
	GetByID(ctx context.Context, id int64) (model.File, error)
	Update(ctx context.Context, id int64, file model.File) error
	DeleteByID(ctx context.Context, id int64) error
	ClearPrimary(ctx context.Context, objectType string, objectID int64, excludeID int64) error
	SetPrimary(ctx context.Context, id int64, objectType string, objectID int64) error
}

// fileStorage описывает абстракцию работы с файловым хранилищем (локальным или external).
type fileStorage interface {
	SaveTemp(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error)
	Delete(ctx context.Context, relativePath string) error
}
