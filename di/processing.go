package di

import (
	logger "github.com/assurrussa/gologger"
	inmemeventstream "github.com/assurrussa/gowebsocket/eventstream/inmem"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/outbox"

	uploadconfig "github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

const KeyFilesUseCaseFinalizeOriginal = "app.files.usecase.finalize_original"

func provideConfiguredUploadService(
	cfg uploadconfig.StorageConfig,
	tx pgsql.TxManager,
	outboxSvc *outbox.Service,
	repo *filerepo.Repo,
	lg logger.Logger,
	storage filestorage.Storage,
) (*uploadservice.Service, error) {
	cfg, err := uploadconfig.NormalizeProcessingConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ProcessingMode == uploadconfig.ProcessingMediaResizer {
		return provideUploadService(tx, outboxSvc, repo, lg, storage)
	}
	return uploadservice.NewWithProcessing(
		uploadservice.NewOptions(tx, outboxSvc, repo, lg.WithNamed("task_uploader"), storage),
		cfg.ProcessingMode,
	)
}

func provideUseCaseFinalizeOriginal(
	cfg uploadconfig.StorageConfig,
	tx pgsql.TxManager,
	outboxSvc *outbox.Service,
	repo *filerepo.Repo,
	lg logger.Logger,
	storage filestorage.Storage,
	events *inmemeventstream.Service,
) (*uploadfile.OriginalUseCase, error) {
	cfg, err := uploadconfig.NormalizeStorageConfig(cfg)
	if err != nil {
		return nil, err
	}
	prefix := cfg.Tus.StagingPrefix
	if prefix == "" {
		prefix = uploadconfig.DefaultStagingPrefix
	}
	return uploadfile.NewOriginal(uploadfile.OriginalOptions{
		Transaction: tx, Repository: repo, Storage: storage, Outbox: outboxSvc,
		Events: events, Logger: lg, BaseFolder: cfg.Public.Prefix,
		DeliveryBaseURL: fileurl.BaseURL(cfg), StagingPrefixes: []string{"tmp/uploads", prefix},
	})
}
