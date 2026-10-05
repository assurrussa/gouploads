package di

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	logger "github.com/assurrussa/gologger"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/outbox"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	uploadconfig "github.com/assurrussa/gouploads/config"
	rcusettings "github.com/assurrussa/gouploads/domain/files/cache/rcu/settings"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/service/fileloader"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	uploadhttp "github.com/assurrussa/gouploads/domain/files/transport/http"
	deletefile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	uploadrawfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_raw_file"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/local"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/sourceurl"
	eventstream "github.com/assurrussa/gouploads/internal/events"
)

func provideFileRepo(db pgsql.Client, tx pgsql.TxManager) (*filerepo.Repo, error) {
	return filerepo.New(filerepo.NewOptions(db, tx))
}

func provideFileLoaderFileRepo(svc *filerepo.Repo) fileloader.FileRepo {
	return svc
}

func provideFileLoader(repo fileloader.FileRepo, lg logger.Logger) (*fileloader.Service, error) {
	return fileloader.New(fileloader.NewOptions(repo, lg))
}

func provideTusStore(cfg uploadconfig.StorageConfig, database pgsql.Client) (tusupload.Store, error) {
	return tusupload.BuildDurableStore(cfg, database)
}

func provideFileStorage(cfg uploadconfig.StorageConfig) (filestorage.Storage, error) {
	switch cfg.Driver {
	case uploadconfig.StorageDriverS3:
		var err error
		cfg, err = uploadconfig.NormalizeStorageConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("validate s3 storage config: %w", err)
		}
		storageCfg := cfg.S3
		httpClient := &http.Client{}
		if storageCfg.Timeout > 0 {
			httpClient.Timeout = storageCfg.Timeout
		}

		awsCfg := aws.Config{
			Region: storageCfg.Region,
			Credentials: credentials.NewStaticCredentialsProvider(
				storageCfg.AccessKey, storageCfg.SecretKey, storageCfg.SessionToken,
			),
			HTTPClient: httpClient,
			Retryer: func() aws.Retryer {
				return retry.NewStandard(func(options *retry.StandardOptions) {
					options.MaxAttempts = storageCfg.MaxRetries + 1
				})
			},
		}

		s3Client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
			o.UsePathStyle = storageCfg.ForcePathStyle
			if storageCfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(storageCfg.Endpoint)
			}
		})

		domain := s3store.NewDomainHost(
			cfg.Public.BaseURL,
			storageCfg.Bucket,
			storageCfg.StagingBucket,
			cfg.Tus.StagingPrefix,
		)

		storage, err := s3store.NewStorageAdapter(s3Client, domain)
		if err != nil {
			return nil, fmt.Errorf("create s3 storage: %w", err)
		}
		return storage, nil
	default:
		storage, err := local.New(cfg.Local.Root, cfg.Local.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("create file storage: %w", err)
		}
		return storage, nil
	}
}

func provideSourceURLResolver(cfg uploadconfig.StorageConfig) (sourceurl.Resolver, error) {
	return sourceurl.New(cfg)
}

func provideUploadService(
	tx pgsql.TxManager,
	outboxSvc *outbox.Service,
	repo *filerepo.Repo,
	lg logger.Logger,
	storage filestorage.Storage,
) (*uploadservice.Service, error) {
	return uploadservice.NewWithProcessing(uploadservice.NewOptions(
		tx,
		outboxSvc,
		repo,
		lg.WithNamed("task_uploader"),
		storage,
	), uploadconfig.ProcessingMediaResizer)
}

func provideTaskUploader(svc *uploadservice.Service) uploadhttp.TaskUploader {
	return svc
}

func provideEventFileAfterProcess(
	cfg uploadconfig.StorageConfig,
	tx pgsql.TxManager,
	repo *filerepo.Repo,
	eventStream eventstream.Publisher,
	lg logger.Logger,
) (*eventfileafterprocess.Service, error) {
	return eventfileafterprocess.New(eventfileafterprocess.NewOptions(
		tx,
		repo,
		eventStream,
		lg,
		eventfileafterprocess.WithDeliveryBaseURL(fileurl.BaseURL(cfg)),
	))
}

func provideResizerSettingsCache(
	ctx context.Context,
	cfg uploadconfig.StorageConfig,
	lg logger.Logger,
) (*rcusettings.CacheService, error) {
	useCase := &staticResizerSettingsUseCase{
		data: rcusettings.Data{
			Enabled:           true,
			ImageResizerToken: strings.TrimSpace(cfg.Image.ResizerToken),
			VideoResizerToken: strings.TrimSpace(cfg.Video.ResizerToken),
		},
	}

	return rcusettings.NewCache(ctx, lg.WithNamed("resizer_settings_cache"), useCase)
}

func provideClientResizer(
	cfg uploadconfig.StorageConfig,
	client *http.Client,
	lg logger.Logger,
	settings *rcusettings.CacheService,
) (*clientresizer.Service, error) {
	return clientresizer.New(clientresizer.NewOptions(
		client,
		cfg.Image.ResizerHost,
		cfg.Video.ResizerHost,
		lg,
		clientresizer.WithImageResizerToken(strings.TrimSpace(cfg.Image.ResizerToken)),
		clientresizer.WithVideoResizerToken(strings.TrimSpace(cfg.Video.ResizerToken)),
		clientresizer.WithTokenProvider(func(ctx context.Context) (imageToken string, videoToken string) {
			return settings.Tokens(ctx)
		}),
	))
}

func provideUseCaseDeleteFile(
	cfg uploadconfig.StorageConfig,
	tx pgsql.TxManager,
	repo *filerepo.Repo,
	eventStream eventstream.Publisher,
	lg logger.Logger,
	storage filestorage.Storage,
	outboxSvc *outbox.Service,
) (*deletefile.UseCase, error) {
	return deletefile.New(deletefile.NewOptions(
		tx,
		repo,
		eventStream,
		lg,
		storage,
		outboxSvc,
		deletefile.WithDeliveryBaseURL(fileurl.BaseURL(cfg)),
	))
}

func provideUseCaseSendResizeFile(
	outboxSvc *outbox.Service,
	listener *listenresizefile.UseCase,
	repo *filerepo.Repo,
	resizer *clientresizer.Service,
	sourceResolver sourceurl.Resolver,
	eventStream eventstream.Publisher,
	uploadCfg uploadconfig.StorageConfig,
	lg logger.Logger,
) (*sendresizefile.UseCase, error) {
	return sendresizefile.New(sendresizefile.NewOptions(
		repo,
		resizer,
		sourceResolver,
		eventStream,
		uploadCfg.Image,
		uploadCfg.Video,
		lg,
		outboxSvc,
		listener,
	))
}

func provideUseCaseListenResizeFile(
	repo *filerepo.Repo,
	outboxSvc *outbox.Service,
	eventStream eventstream.Publisher,
	lg logger.Logger,
) (*listenresizefile.UseCase, error) {
	return listenresizefile.New(listenresizefile.NewOptions(
		repo,
		outboxSvc,
		eventStream,
		lg,
	))
}

func provideUseCaseUploadFile(
	cfg uploadconfig.StorageConfig,
	tx pgsql.TxManager,
	repo *filerepo.Repo,
	resizer *clientresizer.Service,
	eventStream eventstream.Publisher,
	lg logger.Logger,
	storage filestorage.Storage,
	outboxSvc *outbox.Service,
) (*uploadfile.UseCase, error) {
	normalizedCfg, err := uploadconfig.NormalizeStorageConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("normalize upload file storage config: %w", err)
	}
	options := []uploadfile.OptOptionsSetter{
		uploadfile.WithDeliveryBaseURL(fileurl.BaseURL(normalizedCfg)),
	}
	if prefix := strings.TrimSpace(normalizedCfg.Public.Prefix); prefix != "" {
		options = append(options, uploadfile.WithBaseFolder(prefix))
	}

	return uploadfile.New(uploadfile.NewOptions(
		tx,
		repo,
		resizer,
		eventStream,
		lg,
		storage,
		outboxSvc,
		options...,
	))
}

func provideUseCaseUploadRawFile(
	uploadSvc *uploadservice.Service,
	eventStream eventstream.Publisher,
	lg logger.Logger,
) (*uploadrawfile.UseCase, error) {
	return uploadrawfile.New(uploadrawfile.NewOptions(
		uploadSvc,
		eventStream,
		lg,
	))
}

type staticResizerSettingsUseCase struct {
	data rcusettings.Data
}

func (s *staticResizerSettingsUseCase) Handle(context.Context) (rcusettings.Data, error) {
	return s.data, nil
}
