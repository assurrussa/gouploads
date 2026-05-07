package host

import "github.com/assurrussa/gouploads/domain/files/shared/fileurl"

func FilesBaseURL(cfg StorageConfig) string {
	return fileurl.BaseURL(cfg)
}

func FilesBucket(cfg StorageConfig) string {
	return fileurl.Bucket(cfg)
}

func ComposeFileURL(baseURL, bucket, raw string) string {
	return fileurl.Compose(baseURL, bucket, raw)
}

func ComposeFallbackFileURL(baseURL, bucket, raw, fallback string) string {
	return fileurl.ComposeFallback(baseURL, bucket, raw, fallback)
}
