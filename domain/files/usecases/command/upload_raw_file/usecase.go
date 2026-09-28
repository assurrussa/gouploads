package uploadrawfile

import (
	"context"
	"fmt"

	logger "github.com/assurrussa/gologger"
	sharedjob "github.com/assurrussa/outbox/shared/job"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	eventstream "github.com/assurrussa/gouploads/internal/events"
)

//go:generate toolsmocks

type uploadService interface {
	UploadSingle(ctx context.Context, req uploadservice.SingleRequest) (model.File, error)
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	uploadService uploadService         `option:"mandatory" validate:"required"`
	eventStream   eventstream.Publisher `option:"mandatory" validate:"required"`
	logger        logger.Logger         `option:"mandatory" validate:"required"`
}

type UseCase struct {
	sharedjob.DefaultJob
	Options
}

func Must(opts Options) *UseCase {
	j, err := New(opts)
	if err != nil {
		panic(err)
	}

	return j
}

func New(opts Options) (*UseCase, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate job options: %w", err)
	}

	return &UseCase{
		Options: opts,
	}, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (Response, error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}

	fileModel, err := u.uploadService.UploadSingle(ctx, uploadservice.SingleRequest{
		UploaderUUID: req.UploaderUUID,
		ManagerID:    req.ManagerID,
		UserID:       req.UserID,
		FileHeader:   req.FileHeader,
		ObjectType:   req.ObjectType,
		ObjectID:     req.ObjectID,
		DeletedID:    req.DeletedID,
		AfterJobs:    req.AfterJobs,
		Config:       req.Config,
	})
	if err != nil {
		return Response{}, fmt.Errorf("prepare upload config: %w", err)
	}

	return Response{
		File: fileModel,
	}, nil
}
