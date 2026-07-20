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

	"github.com/assurrussa/goshared/pkg/filesanitize"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
)

type s3Client interface {
	CreateMultipartUpload(
		ctx context.Context,
		input *s3.CreateMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.CreateMultipartUploadOutput, error)
	AbortMultipartUpload(
		ctx context.Context,
		input *s3.AbortMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.AbortMultipartUploadOutput, error)
	UploadPart(
		ctx context.Context,
		input *s3.UploadPartInput,
		optFns ...func(*s3.Options),
	) (*s3.UploadPartOutput, error)
	ListParts(
		ctx context.Context,
		input *s3.ListPartsInput,
		optFns ...func(*s3.Options),
	) (*s3.ListPartsOutput, error)
	ListMultipartUploads(
		ctx context.Context,
		input *s3.ListMultipartUploadsInput,
		optFns ...func(*s3.Options),
	) (*s3.ListMultipartUploadsOutput, error)
	CompleteMultipartUpload(
		ctx context.Context,
		input *s3.CompleteMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.CompleteMultipartUploadOutput, error)
	HeadObject(
		ctx context.Context,
		input *s3.HeadObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.HeadObjectOutput, error)
	DeleteObject(
		ctx context.Context,
		input *s3.DeleteObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.DeleteObjectOutput, error)
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

func NewS3Store(
	client s3Client,
	domain s3store.DomainHost,
	repo sessionRepository,
	cfg S3StoreConfig,
) (*S3Store, error) {
	if client == nil {
		return nil, errors.New("tus s3 store: client is required")
	}
	if repo == nil {
		return nil, errors.New("tus s3 store: durable session repository is required")
	}
	if cfg.PartSize <= 0 {
		cfg.PartSize = s3store.MinPartSize
	}
	if cfg.PartSize < s3store.MinPartSize {
		cfg.PartSize = s3store.MinPartSize
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

	return &S3Store{
		client: client,
		domain: domain,
		repo:   repo,
		cfg:    cfg,
	}, nil
}

func (s *S3Store) Create(ctx context.Context, req CreateRequest) (Session, error) {
	if req.UploadLength < 0 {
		return Session{}, fmt.Errorf("invalid upload length: %d", req.UploadLength)
	}
	if strings.TrimSpace(req.OriginalName) == "" {
		return Session{}, errors.New("original name is required")
	}
	if strings.TrimSpace(req.FileName) == "" {
		return Session{}, errors.New("file name is required")
	}

	id := uuidString()
	key, err := buildKey(s.cfg.Prefix, id, req.FileName)
	if err != nil {
		return Session{}, err
	}

	mpu, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:            aws.String(s.domain.StagingBucket()),
		Key:               aws.String(key),
		ChecksumAlgorithm: awss3types.ChecksumAlgorithmSha256,
		CacheControl:      aws.String("private,no-store"),
		ContentType:       aws.String("application/octet-stream"),
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
		UploadID:  uploadID,
		Parts:     make(map[int32]durablePart),
		ExpiresAt: now.Add(s.cfg.TTL),
	}

	if err := s.repo.Create(ctx, session); err != nil {
		_, _ = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.domain.StagingBucket()),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		return Session{}, err
	}

	return session.Session, nil
}

func (s *S3Store) Get(ctx context.Context, id string) (Session, error) {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}

	return session.Session, nil
}

func (s *S3Store) Append(
	ctx context.Context,
	id string,
	offset int64,
	chunk []byte,
	mimeType string,
) (int64, error) {
	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if offset != current.Offset {
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
	newOffset := offset + int64(len(chunk))
	if newOffset > current.UploadLength {
		return current.Offset, ErrLengthExceeded
	}
	isLast := newOffset == current.UploadLength
	if !isLast && int64(len(chunk)) < s.cfg.PartSize {
		return current.Offset, ErrChunkTooSmall
	}
	if !isLast && int64(len(chunk)) != s.cfg.PartSize {
		return current.Offset, ErrChunkSize
	}
	if offset%s.cfg.PartSize != 0 && offset != 0 {
		return current.Offset, ErrOffsetMismatch
	}

	owner := uuid.NewString()
	session, claim, err := s.repo.ClaimAppend(
		ctx,
		id,
		offset,
		owner,
		s.cfg.LeaseTTL,
		s.cfg.TTL,
	)
	if err != nil {
		return current.Offset, err
	}

	partNumber := int32(offset/s.cfg.PartSize + 1)
	checksum := checksumSHA256(chunk)
	part, err := s.reconcileOrUploadPart(ctx, session, partNumber, chunk, checksum)
	if err != nil {
		_ = s.repo.Release(ctx, id, claim)
		return session.Offset, err
	}

	updated, err := s.repo.CommitPart(
		ctx,
		id,
		claim,
		offset,
		part,
		strings.TrimSpace(mimeType),
		s.cfg.TTL,
	)
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
		return s.completeResult(current), nil
	}
	if current.Offset != current.UploadLength {
		return CompleteResult{}, ErrOffsetMismatch
	}

	owner := uuid.NewString()
	session, claim, err := s.repo.ClaimFinalize(
		ctx,
		id,
		owner,
		s.cfg.LeaseTTL,
		s.cfg.TTL,
	)
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
		ready, commitErr := s.repo.CommitFinalize(ctx, id, claim)
		if commitErr != nil {
			return CompleteResult{}, commitErr
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
		Bucket:   aws.String(s.domain.StagingBucket()),
		Key:      aws.String(session.Path),
		UploadId: aws.String(session.UploadID),
		MultipartUpload: &awss3types.CompletedMultipartUpload{
			Parts: completedParts,
		},
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

func (s *S3Store) Delete(ctx context.Context, id string) error {
	return s.deleteSession(ctx, id, false)
}

func (s *S3Store) deleteSession(ctx context.Context, id string, deleteReadyObject bool) error {
	session, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if session.Status != StatusReady && session.UploadID != "" {
		if _, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.domain.StagingBucket()),
			Key:      aws.String(session.Path),
			UploadId: aws.String(session.UploadID),
		}); err != nil && !isNoSuchUpload(err) {
			return fmt.Errorf("abort multipart upload: %w", err)
		}
	}
	if deleteReadyObject {
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.domain.StagingBucket()),
			Key:    aws.String(session.Path),
		}); err != nil {
			return fmt.Errorf("delete staging object: %w", err)
		}
	}

	return s.repo.Delete(ctx, id)
}

func isNoSuchUpload(err error) bool {
	var noSuchUpload *awss3types.NoSuchUpload
	return errors.As(err, &noSuchUpload)
}

func (s *S3Store) Cleanup(ctx context.Context, before time.Time) (int, error) {
	if before.IsZero() {
		return 0, errors.New("cleanup threshold is required")
	}

	const batchSize = 100
	removed := 0
	for {
		ids, err := s.repo.ListExpired(ctx, before, batchSize)
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
			if err := s.deleteSession(ctx, id, true); err != nil && !errors.Is(err, ErrNotFound) {
				return removed, err
			}
			removed++
		}
		if len(ids) < batchSize {
			break
		}
	}

	orphans, err := s.cleanupOrphanMultipartUploads(ctx, before)
	if err != nil {
		return removed, err
	}
	return removed + orphans, nil
}

func (s *S3Store) cleanupOrphanMultipartUploads(ctx context.Context, before time.Time) (int, error) {
	removed := 0
	var keyMarker *string
	var uploadIDMarker *string
	for {
		result, err := s.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket:         aws.String(s.domain.StagingBucket()),
			Prefix:         aws.String(strings.Trim(s.cfg.Prefix, "/") + "/"),
			KeyMarker:      keyMarker,
			UploadIdMarker: uploadIDMarker,
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
			objectPath := aws.ToString(upload.Key)
			uploadID := aws.ToString(upload.UploadId)
			exists, err := s.repo.HasMultipart(ctx, objectPath, uploadID)
			if err != nil {
				return removed, err
			}
			if exists {
				continue
			}
			if _, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.domain.StagingBucket()),
				Key:      aws.String(objectPath),
				UploadId: aws.String(uploadID),
			}); err != nil {
				return removed, fmt.Errorf("abort orphan multipart upload: %w", err)
			}
			removed++
		}
		if !aws.ToBool(result.IsTruncated) {
			return removed, nil
		}
		if result.NextKeyMarker == nil {
			return removed, errors.New("list orphan multipart uploads: truncated result has no next key marker")
		}
		keyMarker = result.NextKeyMarker
		uploadIDMarker = result.NextUploadIdMarker
	}
}

func (s *S3Store) reconcileOrUploadPart(
	ctx context.Context,
	session s3Session,
	partNumber int32,
	chunk []byte,
	checksum string,
) (durablePart, error) {
	remote, found, err := s.findRemotePart(ctx, session, partNumber)
	if err != nil {
		return durablePart{}, err
	}
	if found && remote.Size == int64(len(chunk)) && remote.ChecksumSHA256 == checksum && remote.ETag != "" {
		return remote, nil
	}

	contentLength := int64(len(chunk))
	uploaded, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Body:           bytes.NewReader(chunk),
		Bucket:         aws.String(s.domain.StagingBucket()),
		Key:            aws.String(session.Path),
		PartNumber:     aws.Int32(partNumber),
		UploadId:       aws.String(session.UploadID),
		ContentLength:  &contentLength,
		ChecksumSHA256: aws.String(checksum),
	})
	if err != nil {
		return durablePart{}, fmt.Errorf("upload part %d: %w", partNumber, err)
	}
	etag := strings.TrimSpace(aws.ToString(uploaded.ETag))
	if etag == "" {
		return durablePart{}, fmt.Errorf("upload part %d: empty etag", partNumber)
	}

	return durablePart{
		Number:         partNumber,
		Size:           contentLength,
		ETag:           etag,
		ChecksumSHA256: checksum,
	}, nil
}

func (s *S3Store) findRemotePart(
	ctx context.Context,
	session s3Session,
	partNumber int32,
) (durablePart, bool, error) {
	marker := strconv.FormatInt(int64(partNumber-1), 10)
	maxParts := int32(1)
	result, err := s.client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:           aws.String(s.domain.StagingBucket()),
		Key:              aws.String(session.Path),
		UploadId:         aws.String(session.UploadID),
		PartNumberMarker: aws.String(marker),
		MaxParts:         &maxParts,
	})
	if err != nil {
		return durablePart{}, false, fmt.Errorf("list multipart part %d: %w", partNumber, err)
	}
	if len(result.Parts) == 0 || aws.ToInt32(result.Parts[0].PartNumber) != partNumber {
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
			return nil, errors.New("list multipart parts: truncated result has no next marker")
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
		URL:             "",
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
		// S3-compatible providers are allowed to omit checksum extensions from
		// ListParts. Yandex Object Storage, for example, returns only part number,
		// size, and ETag. Keep checksum validation when the provider exposes it,
		// while treating the durable UploadPart ETag as the remote identity proof.
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

func buildKey(prefix, sessionID, fileName string) (string, error) {
	if strings.TrimSpace(fileName) == "" {
		return "", errors.New("file name is required")
	}

	safeFileName, err := filesanitize.SanitizeFileName(fileName)
	if err != nil {
		return "", err
	}

	ext := strings.TrimPrefix(strings.ToLower(path.Ext(safeFileName)), ".")
	if ext == "" {
		ext = "bin"
	}
	segments := strings.Split(strings.Trim(prefix, "/"), "/")
	segments = append(segments, sessionID)
	safeSegments, err := filesanitize.SanitizeSegments(segments)
	if err != nil {
		return "", err
	}
	safeSegments = append(safeSegments, "source."+ext)
	return strings.Join(safeSegments, "/"), nil
}

func uuidString() string {
	return uuid.NewString()
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return aws.String(value)
}
