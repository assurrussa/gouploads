package uploadstrategies

import (
	"context"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"

	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
)

// UploadContext contains information about the upload request agnostic of the transport layer.
type UploadContext struct {
	UserID    int64
	UserUUID  sharedtypes.UserID
	SessionID string
	Metadata  map[string]string
}

// Strategy defines specific behavior for different upload contexts.
type Strategy interface {
	// CanUpload checks if the user has permission to upload this file.
	CanUpload(ctx context.Context, req UploadContext) error

	// GetConfig returns the upload configuration (allowed extensions, size, etc).
	GetConfig(ctx context.Context, req UploadContext) *uploadservice.FileUploadConfig

	// GetAfterJobs returns jobs to be executed after successful upload.
	GetAfterJobs(ctx context.Context, req UploadContext) ([]fileshared.FileEventAfterJob, error)
}
