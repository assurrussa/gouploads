package config

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	DefaultPublicPrefix    = "media/v1"
	DefaultStagingPrefix   = "staging/v1/tus"
	DefaultS3SourceURLTTL  = 15 * time.Minute
	DefaultS3Timeout       = 30 * time.Second
	DefaultS3MaxRetries    = 10
	maximumS3SourceURLTTL  = 7 * 24 * time.Hour
	maximumS3Timeout       = 5 * time.Minute
	maximumS3RetryAttempts = 50
)

// NormalizeStorageConfig validates the generic S3 contract and applies the
// defaults used by every gouploads S3 entrypoint. The input is copied.
func NormalizeStorageConfig(cfg StorageConfig) (StorageConfig, error) {
	if cfg.Driver != StorageDriverS3 {
		return cfg, nil
	}

	var err error
	cfg.S3.Endpoint, err = normalizeAbsoluteHTTPURL("s3 endpoint", cfg.S3.Endpoint)
	if err != nil {
		return StorageConfig{}, err
	}
	cfg.Public.BaseURL, err = normalizeAbsoluteHTTPURL("public base URL", cfg.Public.BaseURL)
	if err != nil {
		return StorageConfig{}, err
	}
	cfg.Public.Prefix, err = normalizeObjectPrefix("public prefix", cfg.Public.Prefix, DefaultPublicPrefix)
	if err != nil {
		return StorageConfig{}, err
	}
	cfg.Tus.StagingPrefix, err = normalizeObjectPrefix(
		"staging prefix",
		cfg.Tus.StagingPrefix,
		DefaultStagingPrefix,
	)
	if err != nil {
		return StorageConfig{}, err
	}
	if objectPrefixesOverlap(cfg.Public.Prefix, cfg.Tus.StagingPrefix) {
		return StorageConfig{}, errors.New("public and staging prefixes must not overlap")
	}

	cfg.S3.Region = strings.TrimSpace(cfg.S3.Region)
	if cfg.S3.Region == "" {
		return StorageConfig{}, errors.New("s3 region is required")
	}
	cfg.S3.Bucket, err = normalizeBucket("public bucket", cfg.S3.Bucket)
	if err != nil {
		return StorageConfig{}, err
	}
	if strings.TrimSpace(cfg.S3.StagingBucket) == "" {
		cfg.S3.StagingBucket = cfg.S3.Bucket
	} else {
		cfg.S3.StagingBucket, err = normalizeBucket("staging bucket", cfg.S3.StagingBucket)
		if err != nil {
			return StorageConfig{}, err
		}
	}
	if strings.TrimSpace(cfg.S3.AccessKey) == "" {
		return StorageConfig{}, errors.New("s3 access key is required")
	}
	if cfg.S3.SecretKey == "" {
		return StorageConfig{}, errors.New("s3 secret key is required")
	}

	if cfg.S3.SourceURLTTL == 0 {
		cfg.S3.SourceURLTTL = DefaultS3SourceURLTTL
	}
	if cfg.S3.SourceURLTTL < time.Minute || cfg.S3.SourceURLTTL > maximumS3SourceURLTTL {
		return StorageConfig{}, errors.New("s3 source URL TTL must be between 1m and 168h")
	}
	if cfg.S3.Timeout == 0 {
		cfg.S3.Timeout = DefaultS3Timeout
	}
	if cfg.S3.Timeout < time.Second || cfg.S3.Timeout > maximumS3Timeout {
		return StorageConfig{}, errors.New("s3 timeout must be between 1s and 5m")
	}
	if cfg.S3.MaxRetries == 0 {
		cfg.S3.MaxRetries = DefaultS3MaxRetries
	}
	if cfg.S3.MaxRetries < 1 || cfg.S3.MaxRetries > maximumS3RetryAttempts {
		return StorageConfig{}, errors.New("s3 max retries must be between 1 and 50")
	}

	return cfg, nil
}

func normalizeAbsoluteHTTPURL(name, raw string) (string, error) {
	value, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	if value.Scheme != "http" && value.Scheme != "https" {
		return "", fmt.Errorf("%s must use HTTP or HTTPS", name)
	}
	if value.Host == "" || value.User != nil || value.RawQuery != "" || value.Fragment != "" {
		return "", fmt.Errorf("%s must be an absolute URL without userinfo, query, or fragment", name)
	}
	value.Path = strings.TrimRight(value.Path, "/")

	return value.String(), nil
}

func normalizeObjectPrefix(name, raw, fallback string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fallback
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%s must be relative", name)
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s must be a confined non-empty object prefix", name)
	}

	return cleaned, nil
}

func normalizeBucket(name, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if strings.Contains(value, "/") {
		return "", fmt.Errorf("%s must be a bucket name, not a path", name)
	}

	return value, nil
}

func objectPrefixesOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
