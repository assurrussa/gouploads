package eventfileafterprocess

import (
	"context"
	"fmt"
	"log/slog"

	logger "github.com/assurrussa/gologger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	eventstream "github.com/assurrussa/gowebsocket/eventstream"
	sharedjob "github.com/assurrussa/outbox/shared/job"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
)

//go:generate toolsmocks

const (
	loggerName = "service_file_after_process"
)

type fileRepository interface {
	GetByID(ctx context.Context, id int64) (model.File, error)
}

type FnCallAfterProcess func(ctx context.Context, data Payload, file model.File) error

type transactor interface {
	RunInTx(ctx context.Context, f func(context.Context) error) error
}

//go:generate options-gen -out-filename=service_options.gen.go -from-struct=Options
type Options struct {
	transactor         transactor              `option:"mandatory" validate:"required"`
	files              fileRepository          `option:"mandatory" validate:"required"`
	eventStream        eventstream.EventStream `option:"mandatory" validate:"required"`
	logger             logger.Logger           `option:"mandatory" validate:"required"`
	fnCallAfterProcess FnCallAfterProcess
	deliveryBaseURL    string `validate:"omitempty,url"`
}

type Service struct {
	sharedjob.DefaultJob
	Options
}

func Must(opts Options) *Service {
	j, err := New(opts)
	if err != nil {
		panic(err)
	}

	return j
}

func New(opts Options) (*Service, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate service options: %w", err)
	}

	opts.logger = opts.logger.WithNamed(loggerName)

	return &Service{
		Options: opts,
	}, nil
}

func (j *Service) Handle(ctx context.Context, payload string) error {
	return j.handle(ctx, payload, j.fnCallAfterProcess)
}

func (j *Service) HandleAfterProcess(ctx context.Context, payload string, fnCall FnCallAfterProcess) error {
	return j.handle(ctx, payload, fnCall)
}

func (j *Service) handle(ctx context.Context, payload string, fnCall FnCallAfterProcess) (errReturn error) {
	if fnCall == nil {
		j.logger.ErrorContext(ctx, "fnCall function is nil")
		return nil
	}

	data, err := UnmarshalPayload(payload)
	if err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	file, err := j.files.GetByID(ctx, data.FileID)
	if err != nil {
		return fmt.Errorf("%s get file id %d: %w", data.EventType, data.FileID, err)
	}

	taskLogger := j.logger.WithAttrs(
		slog.String("event_type", data.EventType),
		slog.String("user_id", data.UserID.String()),
		slog.Int64("file_id", data.FileID),
		slog.String("object_type", file.ObjectType.String()),
		slog.Int64("object_id", file.ObjectID.Int64()),
	)

	defer func() {
		if errReturn == nil {
			return
		}

		taskLogger.ErrorContext(ctx, "failed deleted file", logger.Error(errReturn))
		j.publish(ctx, data.UserID, j.buildFailedEvent(file, errReturn, data.EventType))
	}()

	err = j.transactor.RunInTx(ctx, func(ctx context.Context) error {
		if err := fnCall(ctx, data, file); err != nil {
			return fmt.Errorf("fn call after process: %w", err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("trx: %w", err)
	}

	j.publish(ctx, data.UserID, j.buildCompletedEvent(file, data.EventType))

	return nil
}

func (j *Service) publish(ctx context.Context, userID sharedtypes.UserID, event shared.EventAfterProcess) {
	if err := event.Validate(); err != nil {
		j.logger.WarnContext(ctx, "invalid event", logger.Error(err))
		return
	}

	if userID.IsZero() {
		j.logger.WarnContext(ctx, "skip publish: empty user id")
		return
	}

	if err := j.eventStream.Publish(ctx, userID, event); err != nil {
		j.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func (j *Service) buildCompletedEvent(file model.File, eventTrigger string) shared.EventAfterProcess {
	return shared.NewEventAfterProcess(file.ID, j.publicURL(file), shared.StatusCompleted, eventTrigger)
}

func (j *Service) buildFailedEvent(file model.File, err error, eventTrigger string) shared.EventAfterProcess {
	event := shared.NewEventAfterProcess(file.ID, j.publicURL(file), shared.StatusFailed, eventTrigger)
	event.Error = err.Error()
	return event
}

func (j *Service) publicURL(file model.File) string {
	return fileurl.Compose(j.deliveryBaseURL, "", file.GetPublicURL())
}
