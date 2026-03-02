package di

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	redis "github.com/assurrussa/goredis"
	"github.com/assurrussa/goshared/pkg/logger"
	transporthttp "github.com/assurrussa/goshared/pkg/transport/http"
	inmemeventstream "github.com/assurrussa/goshared/services/event-stream/in-mem"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/outbox"
	"github.com/aws/aws-sdk-go-v2/aws"
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
	"github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/local"
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

func provideTusStore(cfg uploadconfig.StorageConfig, redisClient redis.ClientShardContract) (tusupload.Store, error) {
	return tusupload.BuildStore(cfg, redisClient)
}

func provideFileStorage(cfg uploadconfig.StorageConfig) (filestorage.Storage, error) {
	storageCfg := cfg.S3
	switch cfg.Driver {
	case uploadconfig.StorageDriverS3:
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
		}

		s3Client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
			o.UsePathStyle = storageCfg.ForcePathStyle
			if storageCfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(storageCfg.Endpoint)
			}
			o.EndpointOptions.DisableHTTPS = storageCfg.DisableSSL
		})

		host := storageCfg.Host
		if host == "" {
			host = storageCfg.Endpoint
		}

		domain := ceph.NewDomainHost(
			host,
			storageCfg.Bucket,
			storageCfg.ACL,
			storageCfg.TransformHost,
			storageCfg.DisableSSL,
			storageCfg.ForcePathStyle,
		)

		storage, err := ceph.NewStorageAdapter(s3Client, domain)
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

func provideUploadService(
	cfg uploadconfig.StorageConfig,
	tx pgsql.TxManager,
	outboxSvc *outbox.Service,
	repo *filerepo.Repo,
	lg logger.Logger,
	storage filestorage.Storage,
) (*uploadservice.Service, error) {
	opts := []uploadservice.OptOptionsSetter{
		uploadservice.WithPublicBucket(fileurl.Bucket(cfg)),
	}
	if cfg.Driver == uploadconfig.StorageDriverS3 {
		opts = append(opts, uploadservice.WithSourceBaseURL(cfg.S3.Endpoint))
	}
	return uploadservice.New(uploadservice.NewOptions(
		tx,
		outboxSvc,
		repo,
		lg.WithNamed("task_uploader"),
		storage,
		fileurl.BaseURL(cfg),
		opts...,
	))
}

func provideTaskUploader(svc *uploadservice.Service) uploadhttp.TaskUploader {
	return svc
}

func provideEventFileAfterProcess(
	tx pgsql.TxManager,
	repo *filerepo.Repo,
	eventStream *inmemeventstream.Service,
	lg logger.Logger,
) (*eventfileafterprocess.Service, error) {
	return eventfileafterprocess.New(eventfileafterprocess.NewOptions(
		tx,
		repo,
		eventStream,
		lg,
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
	client *transporthttp.Client,
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
	tx pgsql.TxManager,
	repo *filerepo.Repo,
	eventStream *inmemeventstream.Service,
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
	))
}

func provideUseCaseSendResizeFile(
	repo *filerepo.Repo,
	resizer *clientresizer.Service,
	eventStream *inmemeventstream.Service,
	uploadCfg uploadconfig.StorageConfig,
	lg logger.Logger,
) (*sendresizefile.UseCase, error) {
	return sendresizefile.New(sendresizefile.NewOptions(
		repo,
		resizer,
		eventStream,
		uploadCfg.Image,
		uploadCfg.Video,
		lg,
	))
}

func provideUseCaseListenResizeFile(
	repo *filerepo.Repo,
	outboxSvc *outbox.Service,
	eventStream *inmemeventstream.Service,
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
	tx pgsql.TxManager,
	repo *filerepo.Repo,
	resizer *clientresizer.Service,
	eventStream *inmemeventstream.Service,
	lg logger.Logger,
	storage filestorage.Storage,
	outboxSvc *outbox.Service,
) (*uploadfile.UseCase, error) {
	return uploadfile.New(uploadfile.NewOptions(
		tx,
		repo,
		resizer,
		eventStream,
		lg,
		storage,
		outboxSvc,
	))
}

func provideUseCaseUploadRawFile(
	uploadSvc *uploadservice.Service,
	eventStream *inmemeventstream.Service,
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
