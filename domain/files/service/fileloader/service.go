package fileloader

import (
	"context"
	"errors"
	"fmt"

	"github.com/assurrussa/goshared/pkg/logger"

	commonmodels "github.com/assurrussa/gouploads/domain/files/model"
)

//go:generate toolsmocks

type FileRepo interface {
	GetByID(ctx context.Context, id int64) (commonmodels.File, error)
}

//go:generate options-gen -out-filename=service_options.gen.go -from-struct=Options
type Options struct {
	fileRepo FileRepo      `option:"mandatory" validate:"required"`
	logger   logger.Logger `option:"mandatory" validate:"required"`
}
type Service struct {
	Options
}

func Must(opts Options) *Service {
	s, err := New(opts)
	if err != nil {
		panic(fmt.Errorf("panic: %w", err))
	}

	return s
}

func New(opts Options) (*Service, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	return &Service{
		Options: opts,
	}, nil
}

// LoadPreview if preview file exists.
func (s *Service) LoadPreview(ctx context.Context, fileID int64) (*commonmodels.File, error) {
	if fileID < 0 {
		return nil, errors.New("fileID should be positive")
	}

	if fileID == 0 {
		return nil, nil //nolint:nilnil // it's valid
	}

	file, err := s.fileRepo.GetByID(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("file repo get by id: %w", err)
	}

	if file.ID == 0 {
		return nil, errors.New("file not found")
	}

	return &file, nil
}

// LoadPreviewURL if preview file exists.
func (s *Service) LoadPreviewURL(ctx context.Context, fileID int64) (string, error) {
	file, err := s.LoadPreview(ctx, fileID)
	if err != nil {
		return "", fmt.Errorf("file repo get by id: %w", err)
	}

	return file.GetPublicURL(), nil
}
