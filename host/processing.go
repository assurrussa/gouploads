package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	eventstream "github.com/assurrussa/gowebsocket/eventstream"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/outbox"
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	uploadconfig "github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	deletefile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
)

type ProcessingMode = uploadconfig.ProcessingMode

const (
	ProcessingOriginalOnly = uploadconfig.ProcessingOriginalOnly
	ProcessingMediaResizer = uploadconfig.ProcessingMediaResizer
)

var ErrProcessingDisabled = uploadconfig.ErrProcessingDisabled

type UploadOutbox interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}

// OriginalRuntimeDeps are application-owned infrastructure. Construct the
// database and transaction manager against the same pool. No resizer, site,
// HTTP client, Redis wrapper or WebSocket server is required here.
type OriginalRuntimeDeps struct {
	Database    pgsql.Client
	Transaction pgsql.TxManager
	Outbox      UploadOutbox
	Logger      logger.Logger
	Events      eventstream.EventStream // Optional; nil disables live events.
}

// OriginalRuntime does not start workers, mount routes or own supplied clients.
// Register Jobs with the application's outbox and keep its worker running.
type OriginalRuntime struct {
	Uploader  *UploadService
	Files     *FileRepo
	Storage   Storage
	TusStore  TusStore
	Finalizer *FinalizeOriginalCommand
	Jobs      []outbox.Job
}

// NewOriginalRuntime builds the default standalone original-only pipeline for
// either local or S3 storage. Empty ProcessingMode means original_only.
func NewOriginalRuntime(cfg StorageConfig, deps OriginalRuntimeDeps) (*OriginalRuntime, error) {
	var err error
	cfg, err = uploadconfig.NormalizeProcessingConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ProcessingMode != ProcessingOriginalOnly {
		return nil, errors.New("NewOriginalRuntime requires original_only; use explicit media pipeline wiring for media_resizer")
	}
	if nilRuntimeDependency(deps.Database) || nilRuntimeDependency(deps.Transaction) || nilRuntimeDependency(deps.Outbox) {
		return nil, errors.New("original runtime requires database, transaction manager and outbox")
	}
	if nilRuntimeDependency(deps.Logger) {
		deps.Logger = logger.Discard()
	}
	if nilRuntimeDependency(deps.Events) {
		deps.Events = discardedUploadEvents{}
	}
	if cfg.Driver == "" {
		cfg.Driver = StorageDriverLocal
	}
	if cfg.Driver != StorageDriverLocal && cfg.Driver != StorageDriverS3 {
		return nil, errors.New("original runtime storage driver must be local or s3")
	}
	if cfg.Driver == StorageDriverLocal && strings.TrimSpace(cfg.Local.Root) == "" {
		return nil, errors.New("original runtime requires an explicit local storage root")
	}
	cfg, err = uploadconfig.NormalizeStorageConfig(cfg)
	if err != nil {
		return nil, err
	}
	storage, err := NewStorage(cfg)
	if err != nil {
		return nil, err
	}
	repo, err := NewFileRepo(deps.Database, deps.Transaction)
	if err != nil {
		return nil, err
	}
	uploader, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(
		deps.Transaction, deps.Outbox, repo, deps.Logger, storage,
	), cfg.ProcessingMode)
	if err != nil {
		return nil, err
	}
	prefix := cfg.Tus.StagingPrefix
	if prefix == "" {
		prefix = uploadconfig.DefaultStagingPrefix
	}
	finalizer, err := uploadfile.NewOriginal(uploadfile.OriginalOptions{
		Transaction: deps.Transaction, Repository: repo, Storage: storage,
		Outbox: deps.Outbox, Events: deps.Events, Logger: deps.Logger,
		BaseFolder: cfg.Public.Prefix, DeliveryBaseURL: fileurl.BaseURL(cfg),
		StagingPrefixes: []string{"tmp/uploads", prefix},
	})
	if err != nil {
		return nil, err
	}
	deleter, err := deletefile.New(deletefile.NewOptions(
		deps.Transaction, repo, deps.Events, deps.Logger, storage, deps.Outbox,
		deletefile.WithDeliveryBaseURL(fileurl.BaseURL(cfg)),
	))
	if err != nil {
		return nil, err
	}
	tusStore, err := NewTusStore(cfg, deps.Database)
	if err != nil {
		return nil, fmt.Errorf("build original TUS store: %w", err)
	}
	jobs, err := BuildOutboxJobs(OutboxJobDeps{
		Logger: deps.Logger, UseCaseFinalizeOriginal: finalizer, UseCaseDeleteFile: deleter,
	})
	if err != nil {
		return nil, err
	}
	return &OriginalRuntime{Uploader: uploader, Files: repo, Storage: storage, TusStore: tusStore, Finalizer: finalizer, Jobs: jobs}, nil
}

// discardedUploadEvents does not acknowledge durable business events; only
// optional best-effort UI updates are omitted. AfterJobs still use the outbox.
type discardedUploadEvents struct{}

func (discardedUploadEvents) Close() error { return nil }
func (discardedUploadEvents) Publish(ctx context.Context, _ sharedtypes.UserID, _ eventstream.Event) error {
	return ctx.Err()
}
func (discardedUploadEvents) Subscribe(context.Context, sharedtypes.UserID) (<-chan eventstream.Event, error) {
	return nil, errors.New("live upload events are disabled")
}

func nilRuntimeDependency(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
