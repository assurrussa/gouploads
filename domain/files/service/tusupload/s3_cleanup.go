package tusupload

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

type cleanupClaim struct {
	Session    s3Session
	Claim      sessionClaim
	KeepObject bool
}
type cleanupRepository interface {
	ClaimCleanup(ctx context.Context, id string, before time.Time, owner string, ttl time.Duration) (cleanupClaim, error)
	DeleteClaimed(ctx context.Context, id string, claim sessionClaim) error
}
type readySessionRefresher interface {
	TouchReady(ctx context.Context, id string, ttl time.Duration) (s3Session, error)
}

func (s *S3Store) Delete(ctx context.Context, id string) error {
	return s.deleteSession(ctx, id, false)
}

func (s *S3Store) deleteSession(ctx context.Context, id string, deleteReadyObject bool) error {
	return s.deleteClaimedSession(ctx, id, time.Time{}, deleteReadyObject)
}

func (s *S3Store) deleteClaimedSession(ctx context.Context, id string, before time.Time, deleteObject bool) error {
	repo, ok := s.repo.(cleanupRepository)
	if !ok {
		return errors.New("S3 cleanup requires a fenced cleanup repository")
	}
	claimed, err := repo.ClaimCleanup(ctx, id, before, uuid.NewString(), s.cfg.LeaseTTL)
	if err != nil {
		return err
	}
	session := claimed.Session
	if session.UploadID != "" {
		_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.domain.StagingBucket()),
			Key:      aws.String(session.Path),
			UploadId: aws.String(session.UploadID),
		})
		if err != nil && !isNoSuchUpload(err) {
			return fmt.Errorf("abort multipart upload: %w", err)
		}
	}
	// Once the durable File/outbox handoff is committed, the finalizer owns
	// this staging key. Expiry of the protocol session must not destroy it.
	if deleteObject && !claimed.KeepObject {
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.domain.StagingBucket()),
			Key:    aws.String(session.Path),
		}); err != nil {
			return fmt.Errorf("delete staging object: %w", err)
		}
	}
	return repo.DeleteClaimed(ctx, id, claimed.Claim)
}

func isNoSuchUpload(err error) bool {
	var missing *awss3types.NoSuchUpload
	if errors.As(err, &missing) {
		return true
	}
	var coded interface{ ErrorCode() string }
	return errors.As(err, &coded) && coded.ErrorCode() == "NoSuchUpload"
}

func (s *S3Store) Cleanup(ctx context.Context, before time.Time) (int, error) {
	if before.IsZero() {
		return 0, errors.New("cleanup threshold is required")
	}
	const batchSize = 100
	expiryBefore := before.Add(s.cfg.TTL)
	removed := 0
	for {
		ids, err := s.repo.ListExpired(ctx, expiryBefore, batchSize)
		if err != nil {
			return removed, err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			err := s.deleteClaimedSession(ctx, id, expiryBefore, true)
			if errors.Is(err, ErrUploadBusy) || errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return removed, err
			}
			removed++
		}
		if len(ids) < batchSize {
			break
		}
	}
	orphans, err := s.cleanupOrphanMultipartUploads(ctx, before)
	return removed + orphans, err
}

func (s *S3Store) cleanupOrphanMultipartUploads(ctx context.Context, before time.Time) (int, error) {
	removed := 0
	var keyMarker, uploadMarker *string
	for {
		result, err := s.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket:         aws.String(s.domain.StagingBucket()),
			Prefix:         aws.String(strings.Trim(s.cfg.Prefix, "/") + "/"),
			KeyMarker:      keyMarker,
			UploadIdMarker: uploadMarker,
		})
		if err != nil {
			return removed, fmt.Errorf("list orphan multipart uploads: %w", err)
		}
		for _, upload := range result.Uploads {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			if upload.Initiated == nil || !upload.Initiated.Before(before) {
				continue
			}
			key, id := aws.ToString(upload.Key), aws.ToString(upload.UploadId)
			exists, err := s.repo.HasMultipart(ctx, key, id)
			if err != nil {
				return removed, err
			}
			if exists {
				continue
			}
			if _, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.domain.StagingBucket()),
				Key:      aws.String(key),
				UploadId: aws.String(id),
			}); err != nil && !isNoSuchUpload(err) {
				return removed, fmt.Errorf("abort orphan multipart upload: %w", err)
			}
			removed++
		}
		if !aws.ToBool(result.IsTruncated) {
			return removed, nil
		}
		if result.NextKeyMarker == nil {
			return removed, errors.New("truncated multipart result has no next key marker")
		}
		keyMarker, uploadMarker = result.NextKeyMarker, result.NextUploadIdMarker
	}
}
