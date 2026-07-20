package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"

	uploadconfig "github.com/assurrussa/gouploads/config"
)

const (
	probeContentType  = "text/plain"
	probeCacheControl = "public,max-age=31536000,immutable"
	maxProbeBodySize  = 1 << 20
)

type StorageContractChecker interface {
	Check(ctx context.Context) (StorageContractReport, error)
}

type StorageContractReport struct {
	ProbeID       string                 `json:"probeId"`
	StagingBucket string                 `json:"stagingBucket"`
	PublicBucket  string                 `json:"publicBucket"`
	StartedAt     time.Time              `json:"startedAt"`
	FinishedAt    time.Time              `json:"finishedAt"`
	Checks        []StorageContractCheck `json:"checks"`
	CleanupErrors []string               `json:"cleanupErrors,omitempty"`
}

type StorageContractCheck struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Detail  string `json:"detail,omitempty"`
}

type storageContractChecker struct {
	client         *awss3.Client
	presigner      *awss3.PresignClient
	httpClient     *http.Client
	endpoint       *url.URL
	publicBaseURL  *url.URL
	publicBucket   string
	stagingBucket  string
	publicPrefix   string
	stagingPrefix  string
	forcePathStyle bool
	sourceURLTTL   time.Duration
}

type storageContractProbe struct {
	stagingKey string
	finalKey   string
	publicURL  string
	body       []byte
	checksum   string
}

type storageContractStep struct {
	name  string
	check func(context.Context, storageContractProbe) error
}

func NewStorageContractChecker(cfg StorageConfig) (StorageContractChecker, error) {
	if cfg.Driver != StorageDriverS3 {
		return nil, errors.New("storage contract checker requires s3 driver")
	}

	cfg, err := uploadconfig.NormalizeStorageConfig(cfg)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(cfg.S3.Endpoint)
	if err != nil {
		return nil, err
	}
	publicBaseURL, err := url.Parse(cfg.Public.BaseURL)
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{Timeout: cfg.S3.Timeout}
	awsConfig := aws.Config{
		Region: cfg.S3.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.S3.AccessKey,
			cfg.S3.SecretKey,
			cfg.S3.SessionToken,
		),
		HTTPClient: httpClient,
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(options *retry.StandardOptions) {
				options.MaxAttempts = cfg.S3.MaxRetries + 1
			})
		},
	}
	client := awss3.NewFromConfig(awsConfig, func(options *awss3.Options) {
		options.UsePathStyle = cfg.S3.ForcePathStyle
		options.BaseEndpoint = aws.String(endpoint.String())
	})

	return &storageContractChecker{
		client:         client,
		presigner:      awss3.NewPresignClient(client),
		httpClient:     httpClient,
		endpoint:       endpoint,
		publicBaseURL:  publicBaseURL,
		publicBucket:   cfg.S3.Bucket,
		stagingBucket:  cfg.S3.StagingBucket,
		publicPrefix:   cfg.Public.Prefix,
		stagingPrefix:  cfg.Tus.StagingPrefix,
		forcePathStyle: cfg.S3.ForcePathStyle,
		sourceURLTTL:   cfg.S3.SourceURLTTL,
	}, nil
}

func (c *storageContractChecker) Check(ctx context.Context) (report StorageContractReport, err error) {
	probeID := uuid.NewString()
	body := []byte("gouploads-storage-contract:" + probeID)
	checksum := sha256.Sum256(body)
	probe := storageContractProbe{
		stagingKey: path.Join(c.stagingPrefix, probeID, "source.txt"),
		finalKey:   path.Join(c.publicPrefix, "storage-check", probeID, "contract.txt"),
		body:       body,
		checksum:   hex.EncodeToString(checksum[:]),
	}
	probe.publicURL = c.publicURL(probe.finalKey)

	report = StorageContractReport{
		ProbeID:       probeID,
		StagingBucket: c.stagingBucket,
		PublicBucket:  c.publicBucket,
		StartedAt:     time.Now().UTC(),
		Checks:        make([]StorageContractCheck, 0, 10),
	}
	defer c.finishCheck(ctx, &report, &err, probe)

	for _, step := range c.contractSteps() {
		checkErr := step.check(ctx, probe)
		report.Checks = append(report.Checks, StorageContractCheck{
			Name:    step.name,
			Success: checkErr == nil,
			Detail:  errorDetail(checkErr),
		})
		if checkErr != nil {
			return report, fmt.Errorf("%s: %w", step.name, checkErr)
		}
	}

	return report, err
}

func (c *storageContractChecker) contractSteps() []storageContractStep {
	return []storageContractStep{
		{name: "staging_put", check: c.checkStagingPut},
		{name: "staging_head", check: c.checkStagingHead},
		{name: "staging_unsigned_get_denied", check: c.checkStagingUnsignedGetDenied},
		{name: "staging_presigned_get", check: c.checkStagingPresignedGet},
		{name: "final_put", check: c.checkFinalPut},
		{name: "public_head", check: c.checkPublicHead},
		{name: "public_get", check: c.checkPublicGet},
		{name: "public_range", check: c.checkPublicRange},
		{name: "authenticated_delete", check: c.checkAuthenticatedDelete},
		{name: "public_unavailable_after_delete", check: c.checkPublicUnavailableAfterDelete},
	}
}

func (c *storageContractChecker) finishCheck(
	ctx context.Context,
	report *StorageContractReport,
	checkErr *error,
	probe storageContractProbe,
) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	for _, target := range []struct {
		bucket string
		key    string
	}{
		{bucket: c.stagingBucket, key: probe.stagingKey},
		{bucket: c.publicBucket, key: probe.finalKey},
	} {
		if err := c.deleteObject(cleanupCtx, target.bucket, target.key); err != nil {
			report.CleanupErrors = append(report.CleanupErrors, err.Error())
		}
	}
	report.FinishedAt = time.Now().UTC()
	if len(report.CleanupErrors) > 0 && *checkErr == nil {
		*checkErr = fmt.Errorf(
			"storage contract cleanup failed: %s",
			strings.Join(report.CleanupErrors, "; "),
		)
	}
}

func (c *storageContractChecker) checkStagingPut(ctx context.Context, probe storageContractProbe) error {
	_, err := c.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:       aws.String(c.stagingBucket),
		Key:          aws.String(probe.stagingKey),
		Body:         bytes.NewReader(probe.body),
		ContentType:  aws.String(probeContentType),
		CacheControl: aws.String("private,no-store"),
	})
	return err
}

func (c *storageContractChecker) checkStagingHead(ctx context.Context, probe storageContractProbe) error {
	head, err := c.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(c.stagingBucket),
		Key:    aws.String(probe.stagingKey),
	})
	if err != nil {
		return err
	}
	if aws.ToInt64(head.ContentLength) != int64(len(probe.body)) {
		return fmt.Errorf(
			"size mismatch: want=%d got=%d",
			len(probe.body),
			aws.ToInt64(head.ContentLength),
		)
	}
	return nil
}

func (c *storageContractChecker) checkStagingUnsignedGetDenied(
	ctx context.Context,
	probe storageContractProbe,
) error {
	response, err := c.do(ctx, http.MethodGet, c.objectURL(c.stagingBucket, probe.stagingKey), "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		return fmt.Errorf("want status 403, got %d", response.StatusCode)
	}
	return nil
}

func (c *storageContractChecker) checkStagingPresignedGet(
	ctx context.Context,
	probe storageContractProbe,
) error {
	presigned, err := c.presigner.PresignGetObject(
		ctx,
		&awss3.GetObjectInput{Bucket: aws.String(c.stagingBucket), Key: aws.String(probe.stagingKey)},
		func(options *awss3.PresignOptions) { options.Expires = c.sourceURLTTL },
	)
	if err != nil {
		return err
	}
	response, err := c.do(ctx, http.MethodGet, presigned.URL, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("want status 200, got %d", response.StatusCode)
	}
	data, err := readProbeBody(response.Body)
	if err != nil {
		return err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != probe.checksum {
		return errors.New("SHA-256 mismatch")
	}
	return nil
}

func (c *storageContractChecker) checkFinalPut(ctx context.Context, probe storageContractProbe) error {
	_, err := c.client.PutObject(ctx, &awss3.PutObjectInput{
		Bucket:       aws.String(c.publicBucket),
		Key:          aws.String(probe.finalKey),
		Body:         bytes.NewReader(probe.body),
		ContentType:  aws.String(probeContentType),
		CacheControl: aws.String(probeCacheControl),
	})
	return err
}

func (c *storageContractChecker) checkPublicHead(ctx context.Context, probe storageContractProbe) error {
	response, err := c.do(ctx, http.MethodHead, probe.publicURL, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("want status 200, got %d", response.StatusCode)
	}
	if c.publicBaseURL.Scheme == "https" && response.TLS == nil {
		return errors.New("HTTPS response has no TLS state")
	}
	if normalizeMediaType(response.Header.Get("Content-Type")) != probeContentType {
		return fmt.Errorf("unexpected content type %q", response.Header.Get("Content-Type"))
	}
	if response.Header.Get("Cache-Control") != probeCacheControl {
		return fmt.Errorf("unexpected cache control %q", response.Header.Get("Cache-Control"))
	}
	return nil
}

func (c *storageContractChecker) checkPublicGet(ctx context.Context, probe storageContractProbe) error {
	response, err := c.do(ctx, http.MethodGet, probe.publicURL, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("want status 200, got %d", response.StatusCode)
	}
	data, err := readProbeBody(response.Body)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, probe.body) {
		return errors.New("public body mismatch")
	}
	return nil
}

func (c *storageContractChecker) checkPublicRange(ctx context.Context, probe storageContractProbe) error {
	response, err := c.do(ctx, http.MethodGet, probe.publicURL, "bytes=0-3")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("want status 206, got %d", response.StatusCode)
	}
	data, err := readProbeBody(response.Body)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, probe.body[:4]) {
		return errors.New("range body mismatch")
	}
	return nil
}

func (c *storageContractChecker) checkAuthenticatedDelete(
	ctx context.Context,
	probe storageContractProbe,
) error {
	if err := c.deleteObject(ctx, c.publicBucket, probe.finalKey); err != nil {
		return err
	}
	return c.deleteObject(ctx, c.stagingBucket, probe.stagingKey)
}

func (c *storageContractChecker) checkPublicUnavailableAfterDelete(
	ctx context.Context,
	probe storageContractProbe,
) error {
	response, err := c.do(ctx, http.MethodGet, probe.publicURL, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return fmt.Errorf("object remains public with status %d", response.StatusCode)
	}
	return nil
}

func (c *storageContractChecker) deleteObject(ctx context.Context, bucket, key string) error {
	_, err := c.client.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (c *storageContractChecker) objectURL(bucket, key string) string {
	value := *c.endpoint
	if c.forcePathStyle {
		value.Path = path.Join(value.Path, bucket, key)
	} else {
		value.Host = bucket + "." + value.Host
		value.Path = path.Join(value.Path, key)
	}
	return value.String()
}

func (c *storageContractChecker) publicURL(key string) string {
	value := *c.publicBaseURL
	value.Path = path.Join(value.Path, key)
	return value.String()
}

func (c *storageContractChecker) do(ctx context.Context, method, rawURL, byteRange string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", method, err)
	}
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf(
			"%s %s: %w",
			method,
			safeRequestURL(request.URL),
			redactRequestError(err),
		)
	}
	return response, nil
}

func readProbeBody(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxProbeBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProbeBodySize {
		return nil, errors.New("probe response is too large")
	}
	return data, nil
}

func normalizeMediaType(raw string) string {
	if separator := strings.IndexByte(raw, ';'); separator >= 0 {
		raw = raw[:separator]
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func safeRequestURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host + value.EscapedPath()
}

func redactRequestError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}

	return err
}

func errorDetail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
