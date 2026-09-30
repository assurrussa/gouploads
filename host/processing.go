package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/outbox"
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	uploadconfig "github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	deletefile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	eventstream "github.com/assurrussa/gouploads/internal/events"
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

// OriginalRuntimeDeps are application-owned infrastructure. The database,
// transaction manager and outbox must share the same transaction context.
type OriginalRuntimeDeps struct {
	Database    pgsql.Client
	Transaction pgsql.TxManager
	Outbox      UploadOutbox
	Logger      logger.Logger
	Events      EventPublisher
	// ContentScanner is optional. When supplied, the finalizer privately spools
	// and scans the same bounded bytes it will publish. Nil is not approval.
	ContentScanner ContentScanner
}

type OriginalRuntime struct {
	Uploader  *UploadService
	Files     *FileRepo
	Storage   Storage
	TusStore  TusStore
	Finalizer *FinalizeOriginalCommand
	Jobs      []outbox.Job
}

// NewOriginalRuntime does not start workers, mount routes or own supplied clients.
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
		deps.Events = eventstream.Discard{}
	}
	if nilRuntimeDependency(deps.ContentScanner) {
		deps.ContentScanner = nil
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
	uploader, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(deps.Transaction,
		deps.Outbox,
		repo,
		deps.Logger,
		storage),
		cfg.ProcessingMode)
	if err != nil {
		return nil, err
	}
	prefix := cfg.Tus.StagingPrefix
	if prefix == "" {
		prefix = uploadconfig.DefaultStagingPrefix
	}
	finalizer, err := uploadfile.NewOriginalWithContentScanner(uploadfile.OriginalOptions{
		Transaction:     deps.Transaction,
		Repository:      repo,
		Storage:         storage,
		Outbox:          deps.Outbox,
		Events:          deps.Events,
		Logger:          deps.Logger,
		BaseFolder:      cfg.Public.Prefix,
		DeliveryBaseURL: fileurl.BaseURL(cfg),
		StagingPrefixes: []string{"tmp/uploads", prefix},
	}, deps.ContentScanner)
	if err != nil {
		return nil, err
	}
	deleter, err := deletefile.New(deletefile.NewOptions(deps.Transaction,
		repo,
		deps.Events,
		deps.Logger,
		storage,
		deps.Outbox,
		deletefile.WithDeliveryBaseURL(fileurl.BaseURL(cfg))))
	if err != nil {
		return nil, err
	}
	tusStore, err := NewTusStore(cfg, deps.Database)
	if err != nil {
		return nil, fmt.Errorf("build original TUS store: %w", err)
	}
	jobs, err := BuildOutboxJobs(OutboxJobDeps{Logger: deps.Logger, UseCaseFinalizeOriginal: finalizer, UseCaseDeleteFile: deleter})
	if err != nil {
		return nil, err
	}
	return &OriginalRuntime{
		Uploader:  uploader,
		Files:     repo,
		Storage:   storage,
		TusStore:  tusStore,
		Finalizer: finalizer,
		Jobs:      jobs,
	}, nil
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
