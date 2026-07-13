package tusupload

import (
	"context"
	"time"
)

type durablePart struct {
	Number         int32  `json:"number"`
	Size           int64  `json:"size"`
	ETag           string `json:"etag"`
	ChecksumSHA256 string `json:"checksumSha256"`
}

type sessionClaim struct {
	Owner string
	Fence int64
}

type sessionRepository interface {
	Create(ctx context.Context, session s3Session) error
	Get(ctx context.Context, id string) (s3Session, error)
	ClaimAppend(
		ctx context.Context,
		id string,
		expectedOffset int64,
		owner string,
		leaseTTL time.Duration,
		sessionTTL time.Duration,
	) (s3Session, sessionClaim, error)
	CommitPart(
		ctx context.Context,
		id string,
		claim sessionClaim,
		expectedOffset int64,
		part durablePart,
		mimeType string,
		sessionTTL time.Duration,
	) (s3Session, error)
	ClaimFinalize(
		ctx context.Context,
		id string,
		owner string,
		leaseTTL time.Duration,
		sessionTTL time.Duration,
	) (s3Session, sessionClaim, error)
	CommitFinalize(ctx context.Context, id string, claim sessionClaim) (s3Session, error)
	Release(ctx context.Context, id string, claim sessionClaim) error
	Delete(ctx context.Context, id string) error
	ListExpired(ctx context.Context, before time.Time, limit int) ([]string, error)
	HasMultipart(ctx context.Context, objectPath string, uploadID string) (bool, error)
}
