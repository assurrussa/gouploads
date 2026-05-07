package host

import (
	"context"
	"time"

	"github.com/assurrussa/goshared/pkg/logger"
	cleanerfiles "github.com/assurrussa/gouploads/domain/files/usecases/command/cleaner_files"
	cleanertusuploads "github.com/assurrussa/gouploads/domain/files/usecases/command/cleaner_tus_uploads"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
)

type (
	CleanFilesRequest  = cleanerfiles.Request
	CleanFilesResponse = cleanerfiles.Response
	CleanFilesUseCase  = cleanerfiles.UseCase
	CleanTusRequest    = cleanertusuploads.Request
	CleanTusResponse   = cleanertusuploads.Response
	CleanTusUseCase    = cleanertusuploads.UseCase
)

type CleanFilesHandler interface {
	Handle(ctx context.Context, req CleanFilesRequest) (CleanFilesResponse, error)
}

type CleanTusHandler interface {
	Handle(ctx context.Context, req CleanTusRequest) (CleanTusResponse, error)
}

func NewCleanTusRequest(before time.Time) CleanTusRequest {
	return CleanTusRequest{Before: before}
}

func NewCleanFilesUseCase(lg logger.Logger, repo *FileRepo, tx pgsql.TxManager) *CleanFilesUseCase {
	return cleanerfiles.Must(cleanerfiles.NewOptions(lg, repo, tx))
}

func NewCleanTusUseCase(store TusStore) *CleanTusUseCase {
	return cleanertusuploads.Must(cleanertusuploads.NewOptions(store))
}
