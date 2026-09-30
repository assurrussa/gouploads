package tusupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	"github.com/assurrussa/gouploads/internal/filesanitize"
)

type s3Client interface {
	CreateMultipartUpload(
		ctx context.Context, input *s3.CreateMultipartUploadInput, options ...func(*s3.Options),
	) (*s3.CreateMultipartUploadOutput, error)
	AbortMultipartUpload(
		ctx context.Context, input *s3.AbortMultipartUploadInput, options ...func(*s3.Options),
	) (*s3.AbortMultipartUploadOutput, error)
	UploadPart(ctx context.Context, input *s3.UploadPartInput, options ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	ListParts(ctx context.Context, input *s3.ListPartsInput, options ...func(*s3.Options)) (*s3.ListPartsOutput, error)
	ListMultipartUploads(
		ctx context.Context, input *s3.ListMultipartUploadsInput, options ...func(*s3.Options),
	) (*s3.ListMultipartUploadsOutput, error)
	CompleteMultipartUpload(
		ctx context.Context, input *s3.CompleteMultipartUploadInput, options ...func(*s3.Options),
	) (*s3.CompleteMultipartUploadOutput, error)
	HeadObject(ctx context.Context, input *s3.HeadObjectInput, options ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, options ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type S3StoreConfig struct {
	Prefix   string
	PartSize int64
	TTL      time.Duration
	LeaseTTL time.Duration
}

const (
	defaultS3StorePrefix = "staging/v1/tus"
	defaultSessionTTL    = 24 * time.Hour
	defaultLeaseTTL      = 30 * time.Second
)

type S3Store struct {
	client s3Client
	domain s3store.DomainHost
	repo   sessionRepository
	cfg    S3StoreConfig
}

func NewS3Store(client s3Client, domain s3store.DomainHost, repo sessionRepository, cfg S3StoreConfig) (*S3Store, error) {
	if client == nil || repo == nil {
		return nil, errors.New("tus s3 store: client and durable repository are required")
	}
	if cfg.PartSize < s3store.MinPartSize {
		cfg.PartSize = s3store.MinPartSize
	}
	if cfg.PartSize > 5<<30 {
		return nil, errors.New("S3 part size exceeds 5 GiB")
	}
	if strings.TrimSpace(cfg.Prefix) == "" {
		cfg.Prefix = defaultS3StorePrefix
	}
	if cfg.TTL <= 0 {
		cfg.TTL = defaultSessionTTL
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = defaultLeaseTTL
	}
	return &S3Store{client: client, domain: domain, repo: repo, cfg: cfg}, nil
}

// ChunkSize advertises the bounded S3 multipart profile to HTTP integrations.
// Intermediate PATCH bodies must match it; only the final body may be shorter.
func (s *S3Store) ChunkSize() int64 { return s.cfg.PartSize }

func (s *S3Store) Create(ctx context.Context, req CreateRequest) (Session, error) {
	parts := req.UploadLength / s.cfg.PartSize
	if req.UploadLength < 0 || parts > 10000 || (parts == 10000 && req.UploadLength%s.cfg.PartSize != 0) {
		return Session{}, errors.New("upload length exceeds the S3 multipart profile")
	}
	if strings.TrimSpace(req.OriginalName) == "" || strings.TrimSpace(req.FileName) == "" {
		return Session{}, errors.New("original name and file name are required")
	}
	id := uuidString()
	key, err := buildKey(s.cfg.Prefix, id, req.FileName)
	if err != nil {
		return Session{}, err
	}
	mpu, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.domain.StagingBucket()), Key: aws.String(key), ChecksumAlgorithm: awss3types.ChecksumAlgorithmSha256,
		CacheControl: aws.String("private,no-store"), ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return Session{}, fmt.Errorf("create multipart upload: %w", err)
	}
	uploadID := strings.TrimSpace(aws.ToString(mpu.UploadId))
	if uploadID == "" {
		return Session{}, errors.New("create multipart upload: empty upload id")
	}
	now := time.Now().UTC()
	session := s3Session{
		Session: Session{
			ID:              id,
			UploadLength:    req.UploadLength,
			Metadata:        cloneMetadata(req.Metadata),
			Path:            key,
			OriginalName:    req.OriginalName,
			FileName:        req.FileName,
			OwnerID:         req.OwnerID,
			OwnerUUID:       req.OwnerUUID,
			CreatedAt:       now,
			UpdatedAt:       now,
			Status:          StatusActive,
			Quarantined:     true,
			FinalizationKey: uuid.NewString(),
		},
		UploadID: uploadID, Parts: make(map[int32]durablePart), ExpiresAt: now.Add(s.cfg.TTL),
	}
	if err := s.repo.Create(ctx, session); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultLeaseTTL)
		defer cancel()
		_, _ = s.client.AbortMultipartUpload(cleanupCtx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.domain.StagingBucket()),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		return Session{}, err
	}
	return session.Session, nil
}

func (s *S3Store) Get(ctx context.Context, id string) (Session, error) {
	if !validSessionID(id) {
		return Session{}, ErrNotFound
	}
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if session.Status == StatusCleaning {
		return Session{}, ErrNotFound
	}
	return session.Session, nil
}

func (s *S3Store) Append(ctx context.Context, id string, offset int64, chunk []byte, mimeType string) (int64, error) {
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if offset < 0 || offset != current.Offset {
		return current.Offset, ErrOffsetMismatch
	}
	if current.Status != StatusActive {
		if current.Status == StatusReady {
			return current.Offset, ErrUploadFinalized
		}
		return current.Offset, ErrUploadBusy
	}
	if len(chunk) == 0 {
		return current.Offset, ErrChunkTooSmall
	}
	if offset > current.UploadLength || int64(len(chunk)) > current.UploadLength-offset {
		return current.Offset, ErrLengthExceeded
	}
	newOffset := offset + int64(len(chunk))
	last := newOffset == current.UploadLength
	if !last && int64(len(chunk)) < s.cfg.PartSize {
		return current.Offset, ErrChunkTooSmall
	}
	if int64(len(chunk)) > s.cfg.PartSize || (!last && int64(len(chunk)) != s.cfg.PartSize) {
		return current.Offset, ErrChunkSize
	}
	if offset%s.cfg.PartSize != 0 {
		return current.Offset, ErrOffsetMismatch
	}
	session, claim, err := s.repo.ClaimAppend(ctx, id, offset, uuid.NewString(), s.cfg.LeaseTTL, s.cfg.TTL)
	if err != nil {
		return current.Offset, err
	}
	part, err := s.reconcileOrUploadPart(ctx, session, int32(offset/s.cfg.PartSize+1), chunk, checksumSHA256(chunk))
	if err != nil {
		_ = s.repo.Release(ctx, id, claim)
		return session.Offset, err
	}
	updated, err := s.repo.CommitPart(ctx, id, claim, offset, part, strings.TrimSpace(mimeType), s.cfg.TTL)
	if err != nil {
		return session.Offset, err
	}
	return updated.Offset, nil
}

func (s *S3Store) Complete(ctx context.Context, id string) (CompleteResult, error) {
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return CompleteResult{}, err
	}
	if current.Status == StatusReady {
		if refresher, ok := s.repo.(readySessionRefresher); ok {
			current, err = refresher.TouchReady(ctx, id, s.cfg.TTL)
			if err != nil {
				return CompleteResult{}, err
			}
		}
		return s.completeResult(current), nil
	}
	if current.Status == StatusCleaning {
		return CompleteResult{}, ErrUploadBusy
	}
	if current.Offset != current.UploadLength {
		return CompleteResult{}, ErrOffsetMismatch
	}
	session, claim, err := s.repo.ClaimFinalize(ctx, id, uuid.NewString(), s.cfg.LeaseTTL, s.cfg.TTL)
	if err != nil {
		if errors.Is(err, ErrUploadFinalized) {
			ready, getErr := s.repo.Get(ctx, id)
			if getErr != nil {
				return CompleteResult{}, getErr
			}
			return s.completeResult(ready), nil
		}
		return CompleteResult{}, err
	}
	parts, listErr := s.listRemoteParts(ctx, session)
	if listErr != nil {
		completed, headErr := s.objectMatchesSession(ctx, session)
		if headErr != nil || !completed {
			_ = s.repo.Release(ctx, id, claim)
			return CompleteResult{}, fmt.Errorf("list multipart parts: %w", listErr)
		}
		ready, err := s.repo.CommitFinalize(ctx, id, claim)
		if err != nil {
			return CompleteResult{}, err
		}
		return s.completeResult(ready), nil
	}
	if err := validateRemoteParts(session, parts); err != nil {
		_ = s.repo.Release(ctx, id, claim)
		return CompleteResult{}, err
	}
	completedParts := make([]awss3types.CompletedPart, 0, len(parts))
	for _, part := range parts {
		completedParts = append(completedParts, awss3types.CompletedPart{
			ETag:           aws.String(part.ETag),
			PartNumber:     aws.Int32(part.Number),
			ChecksumSHA256: optionalString(part.ChecksumSHA256),
		})
	}
	_, completeErr := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(s.domain.StagingBucket()), Key: aws.String(session.Path), UploadId: aws.String(session.UploadID),
		MultipartUpload: &awss3types.CompletedMultipartUpload{Parts: completedParts},
	})
	if completeErr != nil {
		completed, headErr := s.objectMatchesSession(ctx, session)
		if headErr != nil || !completed {
			_ = s.repo.Release(ctx, id, claim)
			return CompleteResult{}, fmt.Errorf("complete multipart upload: %w", completeErr)
		}
	}
	ready, err := s.repo.CommitFinalize(ctx, id, claim)
	if err != nil {
		return CompleteResult{}, err
	}
	return s.completeResult(ready), nil
}

func (s *S3Store) reconcileOrUploadPart(
	ctx context.Context, session s3Session, number int32, chunk []byte, checksum string,
) (durablePart, error) {
	remote, found, err := s.findRemotePart(ctx, session, number)
	if err != nil {
		return durablePart{}, err
	}
	if found && remote.Size == int64(len(chunk)) && remote.ChecksumSHA256 == checksum && remote.ETag != "" {
		return remote, nil
	}
	length := int64(len(chunk))
	part, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Body:           bytes.NewReader(chunk),
		Bucket:         aws.String(s.domain.StagingBucket()),
		Key:            aws.String(session.Path),
		PartNumber:     aws.Int32(number),
		UploadId:       aws.String(session.UploadID),
		ContentLength:  &length,
		ChecksumSHA256: aws.String(checksum),
	})
	if err != nil {
		return durablePart{}, fmt.Errorf("upload part %d: %w", number, err)
	}
	etag := strings.TrimSpace(aws.ToString(part.ETag))
	if etag == "" {
		return durablePart{}, fmt.Errorf("upload part %d: empty etag", number)
	}
	return durablePart{Number: number, Size: length, ETag: etag, ChecksumSHA256: checksum}, nil
}

func (s *S3Store) findRemotePart(ctx context.Context, session s3Session, number int32) (durablePart, bool, error) {
	result, err := s.client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:           aws.String(s.domain.StagingBucket()),
		Key:              aws.String(session.Path),
		UploadId:         aws.String(session.UploadID),
		PartNumberMarker: aws.String(strconv.FormatInt(int64(number-1), 10)),
		MaxParts:         aws.Int32(1),
	})
	if err != nil {
		return durablePart{}, false, fmt.Errorf("list multipart part %d: %w", number, err)
	}
	if len(result.Parts) == 0 || aws.ToInt32(result.Parts[0].PartNumber) != number {
		return durablePart{}, false, nil
	}
	return fromS3Part(result.Parts[0]), true, nil
}

func (s *S3Store) listRemoteParts(ctx context.Context, session s3Session) ([]durablePart, error) {
	parts := make([]durablePart, 0, len(session.Parts))
	var marker *string
	for {
		result, err := s.client.ListParts(ctx, &s3.ListPartsInput{
			Bucket:           aws.String(s.domain.StagingBucket()),
			Key:              aws.String(session.Path),
			UploadId:         aws.String(session.UploadID),
			PartNumberMarker: marker,
		})
		if err != nil {
			return nil, err
		}
		for _, part := range result.Parts {
			parts = append(parts, fromS3Part(part))
		}
		if !aws.ToBool(result.IsTruncated) {
			break
		}
		if strings.TrimSpace(aws.ToString(result.NextPartNumberMarker)) == "" {
			return nil, errors.New("truncated parts result has no next marker")
		}
		marker = result.NextPartNumberMarker
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	return parts, nil
}

func (s *S3Store) objectMatchesSession(ctx context.Context, session s3Session) (bool, error) {
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.domain.StagingBucket()),
		Key:    aws.String(session.Path),
	})
	if err != nil {
		return false, err
	}
	return aws.ToInt64(result.ContentLength) == session.UploadLength, nil
}

func (s *S3Store) completeResult(session s3Session) CompleteResult {
	return CompleteResult{
		Size:            session.UploadLength,
		RelativePath:    session.Path,
		MimeType:        session.MimeType,
		OriginalName:    session.OriginalName,
		FileName:        session.FileName,
		FinalizationKey: session.FinalizationKey,
		Quarantined:     true,
	}
}

type s3Session struct {
	Session
	UploadID  string
	Parts     map[int32]durablePart
	ExpiresAt time.Time
}

func validateRemoteParts(session s3Session, remote []durablePart) error {
	if len(remote) != len(session.Parts) {
		return fmt.Errorf("multipart parts mismatch: durable=%d remote=%d", len(session.Parts), len(remote))
	}
	var total int64
	for _, part := range remote {
		durable, ok := session.Parts[part.Number]
		if !ok {
			return fmt.Errorf("multipart part %d is not recorded", part.Number)
		}
		if durable.Size != part.Size || durable.ETag != part.ETag {
			return fmt.Errorf("multipart part %d does not match durable state", part.Number)
		}
		// Some compatible providers omit checksums from ListParts. Their ETag
		// still has to match the identity recorded from UploadPart.
		if part.ChecksumSHA256 != "" && durable.ChecksumSHA256 != part.ChecksumSHA256 {
			return fmt.Errorf("multipart part %d checksum does not match durable state", part.Number)
		}
		total += part.Size
	}
	if total != session.UploadLength {
		return fmt.Errorf("multipart size mismatch: expected=%d actual=%d", session.UploadLength, total)
	}
	return nil
}

func fromS3Part(part awss3types.Part) durablePart {
	return durablePart{
		Number:         aws.ToInt32(part.PartNumber),
		Size:           aws.ToInt64(part.Size),
		ETag:           aws.ToString(part.ETag),
		ChecksumSHA256: aws.ToString(part.ChecksumSHA256),
	}
}

func checksumSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func buildKey(prefix, id, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("file name is required")
	}
	safe, err := filesanitize.SanitizeFileName(name)
	if err != nil {
		return "", err
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(safe)), ".")
	if ext == "" {
		ext = "bin"
	}
	segments := append(strings.Split(strings.Trim(prefix, "/"), "/"), id)
	segments, err = filesanitize.SanitizeSegments(segments)
	if err != nil {
		return "", err
	}
	return strings.Join(append(segments, "source."+ext), "/"), nil
}
func uuidString() string { return uuid.NewString() }
func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return aws.String(value)
}
