package cleanertusuploads

import (
	"context"
	"fmt"
	"time"
)

//go:generate toolsmocks

type cleaner interface {
	Cleanup(ctx context.Context, before time.Time) (int, error)
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	cleaner cleaner `option:"mandatory" validate:"required"`
}

type UseCase struct {
	cleaner cleaner
}

func Must(opts Options) *UseCase {
	useCase, err := New(opts)
	if err != nil {
		panic(err)
	}
	return useCase
}

func New(opts Options) (*UseCase, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate options: %w", err)
	}

	return &UseCase{
		cleaner: opts.cleaner,
	}, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (Response, error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}

	total, err := u.cleaner.Cleanup(ctx, req.Before)
	if err != nil {
		return Response{}, fmt.Errorf("cleanup tus uploads: %w", err)
	}

	return Response{Total: int64(total)}, nil
}
