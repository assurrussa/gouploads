package fileurl

import (
	"net/url"
	"strings"

	"github.com/assurrussa/gouploads/config"
)

// BaseURL returns the normalized public host for files based on storage configuration.
func BaseURL(cfg config.StorageConfig) string {
	switch cfg.Driver {
	case config.StorageDriverS3:
		if base := sanitizeURL(cfg.S3.Host); base != "" {
			return base
		}
		if base := sanitizeURL(cfg.S3.Endpoint); base != "" {
			return base
		}
	case config.StorageDriverLocal:
		if base := sanitizeLocalURL(cfg.Local.BaseURL, cfg.AppDomainURL); base != "" {
			return base
		}
	}

	return sanitizeURL(cfg.AppDomainURL)
}

// Bucket returns the configured bucket (for S3-like storage).
func Bucket(cfg config.StorageConfig) string {
	if cfg.Driver == config.StorageDriverS3 {
		return strings.Trim(cfg.S3.Bucket, "/")
	}

	return ""
}

func ComposeFallback(baseURL, bucket, raw, fallback string) string {
	if composed := Compose(baseURL, bucket, raw); composed != "" {
		return composed
	}

	return Compose(baseURL, bucket, fallback)
}

// Compose builds an absolute URL for the provided path using base host and optional bucket.
func Compose(baseURL, bucket, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return strings.TrimRight(baseURL, "/")
	}

	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}

	if strings.HasPrefix(raw, "//") {
		if parsed, err := url.Parse(baseURL); err == nil && parsed.Scheme != "" {
			return parsed.Scheme + ":" + raw
		}

		return "https:" + raw
	}

	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return raw
	}

	base = sanitizeURL(base)

	path := raw
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	bucket = strings.Trim(bucket, "/")
	if bucket != "" {
		prefix := "/" + bucket
		if path == prefix { //nolint:revive // it's valid if
			// already at root level
		} else if !strings.HasPrefix(path, prefix+"/") {
			path = prefix + path
		}
	}

	return base + path
}

func sanitizeURL(val string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}

	trimmed := strings.TrimRight(val, "/")
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}

	return "https://" + strings.TrimLeft(trimmed, "/")
}

func sanitizeLocalURL(val, fallback string) string {
	val = strings.TrimSpace(val)
	if val == "" {
		return ""
	}

	if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
		return strings.TrimRight(val, "/")
	}

	base := sanitizeURL(fallback)
	if base == "" {
		return ""
	}

	if strings.HasPrefix(val, "/") {
		return base + val
	}

	return base + "/" + val
}
