package s3store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/filesanitize"
)

type StorageAdapter struct {
	client  Client
	storage S3Storage
	domain  DomainHost
}

const maxDeleteObjects = 1000

func NewStorageAdapter(client Client, domain DomainHost) (*StorageAdapter, error) {
	if client == nil {
		return nil, errors.New("s3 storage: client is required")
	}

	return &StorageAdapter{
		client:  client,
		storage: NewS3Storage(News3Client(client), domain),
		domain:  domain,
	}, nil
}

func (s *StorageAdapter) Exists(ctx context.Context, input filestorage.ExistFileInput) (filestorage.ExistFile, error) {
	key, err := filesanitize.EnsureRelativePath(input.Path)
	if err != nil {
		return filestorage.ExistFile{}, err
	}

	_, err = s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.domain.BucketForKey(key)),
		Key:    aws.String(key),
	})
	if err != nil {
		var nfe *types.NotFound
		if errors.As(err, &nfe) {
			return filestorage.ExistFile{Exist: false}, nil
		}
		return filestorage.ExistFile{}, fmt.Errorf("head object %s: %w", key, err)
	}

	return filestorage.ExistFile{Exist: true}, nil
}

func (s *StorageAdapter) SavePersist(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	relDir, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("s3 storage: dir: %w", err)
	}

	input.Dir = relDir

	return s.saveFile(ctx, input)
}

func (s *StorageAdapter) SaveTemp(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	relDir, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("s3 storage: dir: %w", err)
	}

	if !s.domain.IsStagingKey(relDir) {
		relDir = path.Join(s.domain.StagingPrefix(), relDir)
	}
	input.Dir = relDir

	return s.saveFile(ctx, input)
}

func (s *StorageAdapter) Commit(ctx context.Context, input filestorage.CommitInput) (filestorage.StoredFile, error) {
	if err := input.Validate(); err != nil {
		return filestorage.StoredFile{}, err
	}

	tempKey, err := filesanitize.EnsureRelativePath(input.Path)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("sanitize temp path: %w", err)
	}

	destKey, err := buildKey(input.DestDir, input.FileName)
	if err != nil {
		return filestorage.StoredFile{}, err
	}

	sourceBucket := s.domain.BucketForKey(tempKey)
	destinationBucket := s.domain.Bucket()
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(sourceBucket),
		Key:    aws.String(tempKey),
	})
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("head source object %s: %w", tempKey, err)
	}
	// Match local storage: committing an object onto itself must preserve it.
	// Otherwise the source deletion below would also remove the destination.
	if sourceBucket == destinationBucket && tempKey == destKey {
		return filestorage.StoredFile{
			RelativePath: destKey,
			Size:         aws.ToInt64(head.ContentLength),
			MimeType:     aws.ToString(head.ContentType),
		}, nil
	}

	copySource := encodeCopySource(sourceBucket, tempKey)
	_, err = s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(destinationBucket),
		Key:               aws.String(destKey),
		CopySource:        aws.String(copySource),
		ContentType:       head.ContentType,
		CacheControl:      aws.String(publicCacheControl),
		MetadataDirective: types.MetadataDirectiveReplace,
	})
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("copy object %s -> %s: %w", tempKey, destKey, err)
	}

	if err := s.deleteObjects(ctx, sourceBucket, []string{tempKey}); err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("delete temp object %s: %w", tempKey, err)
	}

	size, mimeType, err := s.objectMetadata(ctx, destinationBucket, destKey)
	if err != nil {
		// metadata lookup failed; continue with defaults
		size = 0
		mimeType = ""
	}

	return filestorage.StoredFile{
		RelativePath: destKey,
		URL:          "",
		Size:         size,
		MimeType:     mimeType,
	}, nil
}

func (s *StorageAdapter) Open(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	key, err := filesanitize.EnsureRelativePath(relativePath)
	if err != nil {
		return nil, fmt.Errorf("sanitize path: %w", err)
	}

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.domain.BucketForKey(key)),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("open object %s: %w", key, err)
	}

	if resp.Body == nil {
		return nil, fmt.Errorf("open object %s: empty body", key)
	}

	return resp.Body, nil
}

func (s *StorageAdapter) Delete(ctx context.Context, relativePath string) error {
	key, err := filesanitize.EnsureRelativePath(relativePath)
	if err != nil {
		return fmt.Errorf("sanitize path: %w", err)
	}

	return s.deleteObjects(ctx, s.domain.BucketForKey(key), []string{key})
}

func (s *StorageAdapter) DeleteBatch(ctx context.Context, relativePaths []string) error {
	if len(relativePaths) == 0 {
		return nil
	}

	keys, err := sanitizeKeys(relativePaths)
	if err != nil {
		return err
	}

	byBucket := make(map[string][]string, 2)
	for _, key := range keys {
		bucket := s.domain.BucketForKey(key)
		byBucket[bucket] = append(byBucket[bucket], key)
	}
	for bucket, bucketKeys := range byBucket {
		if err := s.deleteObjects(ctx, bucket, bucketKeys); err != nil {
			return err
		}
	}

	return nil
}

func sanitizeKeys(paths []string) ([]string, error) {
	keys := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))

	for _, raw := range paths {
		key, err := filesanitize.EnsureRelativePath(raw)
		if err != nil {
			return nil, fmt.Errorf("sanitize path: %w", err)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	return keys, nil
}

func (s *StorageAdapter) deleteObjects(ctx context.Context, bucket string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	for start := 0; start < len(keys); start += maxDeleteObjects {
		end := start + maxDeleteObjects
		if end > len(keys) {
			end = len(keys)
		}

		batchKeys := keys[start:end]
		objects := make([]types.ObjectIdentifier, len(batchKeys))
		for i, key := range batchKeys {
			objects[i] = types.ObjectIdentifier{Key: aws.String(key)}
		}

		output, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: objects},
		})
		if err != nil {
			if len(keys) == 1 {
				return fmt.Errorf("delete object %s: %w", keys[0], err)
			}

			first := ""
			if len(batchKeys) > 0 {
				first = batchKeys[0]
			}
			return fmt.Errorf("delete objects batch starting with %s: %w", first, err)
		}
		if output != nil && len(output.Errors) > 0 {
			failure := output.Errors[0]
			return fmt.Errorf(
				"delete objects: provider rejected %d object(s), first key=%q code=%q message=%q",
				len(output.Errors),
				aws.ToString(failure.Key),
				aws.ToString(failure.Code),
				aws.ToString(failure.Message),
			)
		}
	}

	return nil
}

func (s *StorageAdapter) objectMetadata(ctx context.Context, bucket, key string) (int64, string, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Range:  aws.String("bytes=0-0"),
	})
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	if _, err = io.Copy(io.Discard, resp.Body); err != nil {
		return 0, "", err
	}

	size := parseContentRange(resp.ContentRange)
	if size == 0 && resp.ContentLength != nil {
		size = *resp.ContentLength
	}

	mimeType := ""
	if resp.ContentType != nil {
		mimeType = *resp.ContentType
	}

	return size, mimeType, nil
}

func (s *StorageAdapter) saveFile(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	if err := input.Validate(); err != nil {
		return filestorage.StoredFile{}, err
	}

	key, err := buildKey(input.Dir, input.FileName)
	if err != nil {
		return filestorage.StoredFile{}, err
	}

	counter := &countingReader{Reader: input.Reader}
	_, err = s.storage.UploadFile(ctx, counter, key, input.MimeType)
	if err != nil {
		return filestorage.StoredFile{}, err
	}

	return filestorage.StoredFile{
		RelativePath: key,
		URL:          "",
		Size:         counter.Size(),
		MimeType:     input.MimeType,
	}, nil
}

func buildKey(dir, fileName string) (string, error) {
	cleanFile := strings.TrimSpace(fileName)
	if cleanFile == "" {
		return "", errors.New("file name is required")
	}

	sanitizedFile, err := filesanitize.SanitizeFileName(cleanFile)
	if err != nil {
		return "", fmt.Errorf("sanitize file name: %w", err)
	}

	cleanDir := strings.TrimSpace(dir)
	if cleanDir == "" {
		return sanitizedFile, nil
	}

	dirPath, err := filesanitize.EnsureRelativePath(cleanDir)
	if err != nil {
		return "", fmt.Errorf("sanitize dir: %w", err)
	}

	joined := path.Join(dirPath, sanitizedFile)
	return filesanitize.EnsureRelativePath(joined)
}

func encodeCopySource(bucket, key string) string {
	segments := strings.Split(key, "/")
	encoded := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		encoded = append(encoded, url.PathEscape(segment))
	}
	return bucket + "/" + strings.Join(encoded, "/")
}

func parseContentRange(contentRange *string) int64 {
	if contentRange == nil {
		return 0
	}
	value := *contentRange
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return 0
	}
	total := strings.TrimSpace(parts[1])
	if total == "*" {
		return 0
	}
	size, err := strconv.ParseInt(total, 10, 64)
	if err != nil {
		return 0
	}
	return size
}

func sanitizeRelativePath(p string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if value == "" {
		return "", errors.New("empty path")
	}

	clean := path.Clean(value)
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || clean == "" {
		return "", errors.New("path resolves to root")
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		return "", errors.New("path escapes root")
	}

	return clean, nil
}

type countingReader struct {
	io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) Size() int64 {
	return c.n
}
