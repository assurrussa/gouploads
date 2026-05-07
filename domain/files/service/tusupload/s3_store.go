package tusupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/assurrussa/goshared/pkg/filesanitize"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	redislib "github.com/redis/go-redis/v9"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
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
	CompleteMultipartUpload(
		ctx context.Context,
		input *s3.CompleteMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.CompleteMultipartUploadOutput, error)
}

type S3StoreConfig struct {
	Prefix   string
	PartSize int64
	TTL      time.Duration
	IndexKey string
}

const defaultS3StorePrefix = "tmp/uploads"

type S3Store struct {
	client s3Client
	domain ceph.DomainHost
	redis  redisClient
	cfg    S3StoreConfig
}

func NewS3Store(client s3Client, domain ceph.DomainHost, redis redisClient, cfg S3StoreConfig) (*S3Store, error) {
	if client == nil {
		return nil, errors.New("tus s3 store: client is required")
	}
	if redis == nil {
		return nil, errors.New("tus s3 store: redis is required")
	}
	if cfg.PartSize <= 0 {
		cfg.PartSize = ceph.MinPartSize
	}
	if cfg.PartSize < ceph.MinPartSize {
		cfg.PartSize = ceph.MinPartSize
	}
	if cfg.Prefix == "" {
		cfg.Prefix = defaultS3StorePrefix
	}

	return &S3Store{
		client: client,
		domain: domain,
		redis:  redis,
		cfg:    cfg,
	}, nil
}

func (s *S3Store) Create(ctx context.Context, req CreateRequest) (Session, error) {
	if req.UploadLength < 0 {
		return Session{}, fmt.Errorf("invalid upload length: %d", req.UploadLength)
	}
	if req.OriginalName == "" {
		return Session{}, errors.New("original name is required")
	}
	if req.FileName == "" {
		return Session{}, errors.New("file name is required")
	}

	id := uuidString()
	key, err := buildKey(s.cfg.Prefix, req.Metadata, req.FileName)
	if err != nil {
		return Session{}, err
	}

	now := time.Now()
	session := s3Session{
		Session: Session{
			ID:           id,
			UploadLength: req.UploadLength,
			Offset:       0,
			Metadata:     cloneMetadata(req.Metadata),
			Path:         key,
			URL:          s.buildURL(key),
			OriginalName: req.OriginalName,
			FileName:     req.FileName,
			OwnerID:      req.OwnerID,
			OwnerUUID:    req.OwnerUUID,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		Parts: make(map[int32]string),
	}

	if err := s.saveSession(ctx, session); err != nil {
		return Session{}, err
	}

	return session.Session, nil
}

func (s *S3Store) Get(ctx context.Context, id string) (Session, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return Session{}, err
	}

	return session.Session, nil
}

func (s *S3Store) Append(ctx context.Context, id string, offset int64, chunk []byte, mimeType string) (int64, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return 0, err
	}

	if offset != session.Offset {
		return session.Offset, ErrOffsetMismatch
	}

	if session.UploadLength >= 0 && offset > session.UploadLength {
		return session.Offset, ErrLengthExceeded
	}

	if len(chunk) == 0 {
		return session.Offset, ErrChunkTooSmall
	}

	isLast := session.UploadLength >= 0 && offset+int64(len(chunk)) == session.UploadLength
	if !isLast && int64(len(chunk)) < s.cfg.PartSize {
		return session.Offset, ErrChunkTooSmall
	}

	if offset%s.cfg.PartSize != 0 && offset != 0 {
		return session.Offset, ErrOffsetMismatch
	}

	if session.UploadID == "" {
		contentType := strings.TrimSpace(mimeType)
		if contentType == "" {
			contentType = session.MimeType
		}
		mpu, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
			ACL:         awss3types.ObjectCannedACL(s.domain.ACL()),
			Bucket:      aws.String(s.domain.Bucket()),
			Key:         aws.String(session.Path),
			ContentType: optionalString(contentType),
		})
		if err != nil {
			return session.Offset, fmt.Errorf("create multipart upload: %w", err)
		}

		session.UploadID = aws.ToString(mpu.UploadId)
		if contentType != "" {
			session.MimeType = contentType
		}
	}

	partNumber := int32(offset/s.cfg.PartSize + 1)
	contentLength := int64(len(chunk))
	uploadedPart, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Body:          bytes.NewReader(chunk),
		Bucket:        aws.String(s.domain.Bucket()),
		Key:           aws.String(session.Path),
		PartNumber:    aws.Int32(partNumber),
		UploadId:      aws.String(session.UploadID),
		ContentLength: &contentLength,
	})
	if err != nil {
		return session.Offset, fmt.Errorf("upload part %d: %w", partNumber, err)
	}

	session.Parts[partNumber] = aws.ToString(uploadedPart.ETag)
	session.Offset = offset + int64(len(chunk))
	session.UpdatedAt = time.Now()

	if err := s.saveSession(ctx, session); err != nil {
		return session.Offset, err
	}

	return session.Offset, nil
}

func (s *S3Store) Complete(ctx context.Context, id string) (CompleteResult, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		return CompleteResult{}, err
	}

	if session.UploadLength >= 0 && session.Offset < session.UploadLength {
		return CompleteResult{}, ErrOffsetMismatch
	}

	if session.UploadID == "" {
		return CompleteResult{}, errors.New("multipart upload is not initialized")
	}

	parts := make([]awss3types.CompletedPart, 0, len(session.Parts))
	partNumbers := make([]int, 0, len(session.Parts))
	for partNumber := range session.Parts {
		partNumbers = append(partNumbers, int(partNumber))
	}
	sort.Ints(partNumbers)

	for _, number := range partNumbers {
		partNumber := int32(number)
		etag := session.Parts[partNumber]
		if etag == "" {
			continue
		}
		parts = append(parts, awss3types.CompletedPart{
			ETag:       aws.String(etag),
			PartNumber: aws.Int32(partNumber),
		})
	}

	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.domain.Bucket()),
		Key:      aws.String(session.Path),
		UploadId: aws.String(session.UploadID),
		MultipartUpload: &awss3types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		return CompleteResult{}, fmt.Errorf("complete multipart upload: %w", err)
	}

	if err := s.deleteSession(ctx, id); err != nil {
		return CompleteResult{}, err
	}

	return CompleteResult{
		Size:         session.UploadLength,
		RelativePath: session.Path,
		URL:          session.URL,
		MimeType:     session.MimeType,
		OriginalName: session.OriginalName,
		FileName:     session.FileName,
	}, nil
}

func (s *S3Store) Delete(ctx context.Context, id string) error {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	if session.UploadID != "" {
		_, _ = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(s.domain.Bucket()),
			Key:      aws.String(session.Path),
			UploadId: aws.String(session.UploadID),
		})
	}

	return s.deleteSession(ctx, id)
}

type s3Session struct {
	Session
	UploadID string
	Parts    map[int32]string
}

func (s *S3Store) buildURL(key string) string {
	link, _ := url.JoinPath(s.domain.Host(), s.domain.Bucket(), key)
	return link
}

func (s *S3Store) redisKey(id string) string {
	return "tus:upload:" + id
}

func (s *S3Store) indexKey() string {
	if strings.TrimSpace(s.cfg.IndexKey) != "" {
		return s.cfg.IndexKey
	}
	return "tus:uploads:index"
}

func (s *S3Store) saveSession(ctx context.Context, session s3Session) error {
	data, err := json.Marshal(toSessionDTO(session))
	if err != nil {
		return fmt.Errorf("marshal tus session: %w", err)
	}

	ttl := s.cfg.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	if err := s.redis.Set(ctx, s.redisKey(session.ID), data, ttl).Err(); err != nil {
		return fmt.Errorf("save tus session: %w", err)
	}
	if err := s.redis.ZAdd(ctx, s.indexKey(), redislib.Z{
		Score:  float64(session.UpdatedAt.Unix()),
		Member: session.ID,
	}).Err(); err != nil {
		return fmt.Errorf("save tus session: %w", err)
	}

	return nil
}

func (s *S3Store) loadSession(ctx context.Context, id string) (s3Session, error) {
	data, err := s.redis.Get(ctx, s.redisKey(id)).Bytes()
	if err != nil {
		if errors.Is(err, redislib.Nil) {
			return s3Session{}, ErrNotFound
		}
		return s3Session{}, fmt.Errorf("load tus session: %w", err)
	}

	var dto s3SessionDTO
	if err := json.Unmarshal(data, &dto); err == nil {
		return dto.toSession(), nil
	}

	var legacy s3SessionLegacy
	if err := json.Unmarshal(data, &legacy); err != nil {
		return s3Session{}, fmt.Errorf("unmarshal tus session: %w", err)
	}

	return legacy.toSession(), nil
}

func (s *S3Store) deleteSession(ctx context.Context, id string) error {
	if err := s.redis.Del(ctx, s.redisKey(id)).Err(); err != nil {
		return fmt.Errorf("delete tus session: %w", err)
	}
	if err := s.redis.ZRem(ctx, s.indexKey(), id).Err(); err != nil {
		return fmt.Errorf("delete tus session: %w", err)
	}

	return nil
}

func (s *S3Store) Cleanup(ctx context.Context, before time.Time) (int, error) {
	if before.IsZero() {
		return 0, errors.New("cleanup threshold is required")
	}

	const batchSize = 100
	removed := 0
	for {
		if err := ctx.Err(); err != nil {
			return removed, err
		}

		ids, err := s.loadExpiredIDs(ctx, before, batchSize)
		if err != nil {
			return removed, fmt.Errorf("load tus cleanup index: %w", err)
		}
		if len(ids) == 0 {
			break
		}

		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return removed, err
			}

			cleaned, err := s.cleanupSession(ctx, id)
			if err != nil {
				return removed, err
			}
			if cleaned {
				removed++
			}
		}

		if len(ids) < batchSize {
			break
		}
	}

	return removed, nil
}

func (s *S3Store) loadExpiredIDs(ctx context.Context, before time.Time, limit int64) ([]string, error) {
	return s.redis.ZRangeByScore(ctx, s.indexKey(), &redislib.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(before.Unix(), 10),
		Offset: 0,
		Count:  limit,
	}).Result()
}

func (s *S3Store) cleanupSession(ctx context.Context, id string) (bool, error) {
	session, err := s.loadSession(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			if err := s.redis.ZRem(ctx, s.indexKey(), id).Err(); err != nil {
				return false, fmt.Errorf("cleanup tus index: %w", err)
			}
			return false, nil
		}
		return false, err
	}

	if err := s.abortMultipartIfNeeded(ctx, session); err != nil {
		return false, err
	}

	if err := s.deleteSession(ctx, id); err != nil {
		return false, err
	}

	return true, nil
}

func (s *S3Store) abortMultipartIfNeeded(ctx context.Context, session s3Session) error {
	if session.UploadID == "" {
		return nil
	}

	if _, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.domain.Bucket()),
		Key:      aws.String(session.Path),
		UploadId: aws.String(session.UploadID),
	}); err != nil {
		return fmt.Errorf("abort multipart upload: %w", err)
	}

	return nil
}

func buildKey(prefix string, metadata map[string]string, fileName string) (string, error) {
	if strings.TrimSpace(fileName) == "" {
		return "", errors.New("file name is required")
	}

	safeFileName, err := filesanitize.SanitizeFileName(fileName)
	if err != nil {
		return "", err
	}

	objectType := strings.TrimSpace(metadata["entity_type"])
	objectID := strings.TrimSpace(metadata["entity_id"])
	baseSegments := strings.Split(strings.Trim(prefix, "/"), "/")
	segments := append([]string{}, baseSegments...)
	if objectType != "" {
		segments = append(segments, objectType)
	}
	if objectID != "" {
		segments = append(segments, objectID)
	}
	segments = append(segments, uuidString())
	safeSegments, err := filesanitize.SanitizeSegments(segments)
	if err != nil {
		return "", err
	}
	safeSegments = append(safeSegments, safeFileName)
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

type s3SessionDTO struct {
	ID           string             `json:"id"`
	UploadLength int64              `json:"uploadLength"`
	Offset       int64              `json:"offset"`
	Metadata     map[string]string  `json:"metadata,omitempty"`
	Path         string             `json:"path"`
	URL          string             `json:"url,omitempty"`
	OriginalName string             `json:"originalName"`
	FileName     string             `json:"fileName"`
	MimeType     string             `json:"mimeType,omitempty"`
	OwnerID      int64              `json:"ownerId"`
	OwnerUUID    sharedtypes.UserID `json:"ownerUuid"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	UploadID     string             `json:"uploadId"`
	Parts        map[int32]string   `json:"parts,omitempty"`
}

func toSessionDTO(session s3Session) s3SessionDTO {
	return s3SessionDTO{
		ID:           session.ID,
		UploadLength: session.UploadLength,
		Offset:       session.Offset,
		Metadata:     session.Metadata,
		Path:         session.Path,
		URL:          session.URL,
		OriginalName: session.OriginalName,
		FileName:     session.FileName,
		MimeType:     session.MimeType,
		OwnerID:      session.OwnerID,
		OwnerUUID:    session.OwnerUUID,
		CreatedAt:    session.CreatedAt,
		UpdatedAt:    session.UpdatedAt,
		UploadID:     session.UploadID,
		Parts:        session.Parts,
	}
}

func (dto s3SessionDTO) toSession() s3Session {
	parts := dto.Parts
	if parts == nil {
		parts = make(map[int32]string)
	}
	return s3Session{
		Session: Session{
			ID:           dto.ID,
			UploadLength: dto.UploadLength,
			Offset:       dto.Offset,
			Metadata:     dto.Metadata,
			Path:         dto.Path,
			URL:          dto.URL,
			OriginalName: dto.OriginalName,
			FileName:     dto.FileName,
			MimeType:     dto.MimeType,
			OwnerID:      dto.OwnerID,
			OwnerUUID:    dto.OwnerUUID,
			CreatedAt:    dto.CreatedAt,
			UpdatedAt:    dto.UpdatedAt,
		},
		UploadID: dto.UploadID,
		Parts:    parts,
	}
}

//nolint:tagliatelle // maintain legacy snake_case fields for compatibility with stored sessions
type s3SessionLegacy struct {
	ID           string             `json:"ID"`
	UploadLength int64              `json:"UploadLength"`
	Offset       int64              `json:"Offset"`
	Metadata     map[string]string  `json:"Metadata"`
	Path         string             `json:"Path"`
	URL          string             `json:"URL"`
	OriginalName string             `json:"OriginalName"`
	FileName     string             `json:"FileName"`
	MimeType     string             `json:"MimeType"`
	OwnerID      int64              `json:"OwnerID"`
	OwnerUUID    sharedtypes.UserID `json:"OwnerUUID"`
	CreatedAt    time.Time          `json:"CreatedAt"`
	UpdatedAt    time.Time          `json:"UpdatedAt"`
	UploadID     string             `json:"upload_id"`
	Parts        map[int32]string   `json:"parts"`
}

func (legacy s3SessionLegacy) toSession() s3Session {
	parts := legacy.Parts
	if parts == nil {
		parts = make(map[int32]string)
	}
	return s3Session{
		Session: Session{
			ID:           legacy.ID,
			UploadLength: legacy.UploadLength,
			Offset:       legacy.Offset,
			Metadata:     legacy.Metadata,
			Path:         legacy.Path,
			URL:          legacy.URL,
			OriginalName: legacy.OriginalName,
			FileName:     legacy.FileName,
			MimeType:     legacy.MimeType,
			OwnerID:      legacy.OwnerID,
			OwnerUUID:    legacy.OwnerUUID,
			CreatedAt:    legacy.CreatedAt,
			UpdatedAt:    legacy.UpdatedAt,
		},
		UploadID: legacy.UploadID,
		Parts:    parts,
	}
}
