//nolint:lll // it's config
package config

import "time"

const (
	StorageDriverLocal = "local"
	StorageDriverS3    = "s3"
)

type StorageConfig struct {
	AppDomainURL string              `toml:"app_domain_url" long:"app-domain-url" env:"APP_DOMAIN_URL" value-default:"https://localhost"`
	Driver       string              `toml:"driver" long:"storage-driver" env:"STORAGE_DRIVER" value-default:"local" validate:"required,oneof=local s3"`
	Local        StorageLocalConfig  `toml:"local"`
	S3           StorageS3Config     `toml:"s3"`
	Image        ImagePipelineConfig `toml:"image_pipeline"`
	Video        VideoPipelineConfig `toml:"video_pipeline"`
	Tus          StorageTusConfig    `toml:"tus"`
}

type StorageLocalConfig struct {
	Root    string `toml:"root" long:"storage-local-root" env:"STORAGE_LOCAL_ROOT" value-default:"public"`
	BaseURL string `toml:"base_url" long:"storage-local-base-url" env:"STORAGE_LOCAL_BASE_URL" value-default:""`
}

type StorageS3Config struct {
	Endpoint       string        `toml:"endpoint" long:"storage-s3-endpoint" env:"STORAGE_S3_ENDPOINT"`
	Host           string        `toml:"host" long:"storage-s3-host" env:"STORAGE_S3_HOST"`
	SourceHost     string        `toml:"source_host" long:"storage-s3-source-host" env:"STORAGE_S3_SOURCE_HOST" validate:"omitempty,url"`
	SourceURLTTL   time.Duration `toml:"source_url_ttl" long:"storage-s3-source-url-ttl" env:"STORAGE_S3_SOURCE_URL_TTL" value-default:"6h" validate:"min=1m,max=168h"`
	ACL            string        `toml:"acl" long:"storage-s3-acl" env:"STORAGE_S3_ACL" value-default:"public-read"`
	Region         string        `toml:"region" long:"storage-s3-region" env:"STORAGE_S3_REGION"`
	Bucket         string        `toml:"bucket" long:"storage-s3-bucket" env:"STORAGE_S3_BUCKET"`
	AccessKey      string        `toml:"access_key" long:"storage-s3-access-key" env:"STORAGE_S3_ACCESS_KEY"`
	SecretKey      string        `toml:"secret_key" long:"storage-s3-secret-key" env:"STORAGE_S3_SECRET_KEY"`
	SessionToken   string        `toml:"session_token" long:"storage-s3-session-token" env:"STORAGE_S3_SESSION_TOKEN"`
	ForcePathStyle bool          `toml:"force_path_style" long:"storage-s3-force-path-style" env:"STORAGE_S3_FORCE_PATH_STYLE" value-default:"false"`
	TransformHost  bool          `toml:"transform_host" long:"storage-s3-transform-host" env:"STORAGE_S3_TRANSFORM_HOST" value-default:"true"`
	DisableSSL     bool          `toml:"disable_ssl" long:"storage-s3-disable-ssl" env:"STORAGE_S3_DISABLE_SSL" value-default:"false"`
	Timeout        time.Duration `toml:"timeout" long:"storage-s3-timeout" env:"STORAGE_S3_TIMEOUT" value-default:"20s" validate:"min=1s,max=5m"`
	MaxRetries     int           `toml:"max_retries" long:"storage-s3-max-retries" env:"STORAGE_S3_MAX_RETRIES" value-default:"10" validate:"min=1,max=50"`
}

type StorageTusConfig struct {
	PartSize         ParseSize     `toml:"part_size" long:"storage-tus-part-size" env:"STORAGE_TUS_PART_SIZE" value-default:"5MB"`
	SessionTTL       time.Duration `toml:"session_ttl" long:"storage-tus-session-ttl" env:"STORAGE_TUS_SESSION_TTL" value-default:"24h" validate:"min=1m"`
	LeaseTTL         time.Duration `toml:"lease_ttl" long:"storage-tus-lease-ttl" env:"STORAGE_TUS_LEASE_TTL" value-default:"30s" validate:"min=1s,max=5m"`
	QuarantinePrefix string        `toml:"quarantine_prefix" long:"storage-tus-quarantine-prefix" env:"STORAGE_TUS_QUARANTINE_PREFIX" value-default:"quarantine/uploads" validate:"required"`
	CleanupInterval  time.Duration `toml:"cleanup_interval" long:"storage-tus-cleanup-interval" env:"STORAGE_TUS_CLEANUP_INTERVAL" value-default:"1h" validate:"min=1m"`
	CleanupSpec      string        `toml:"cleanup_spec" long:"storage-tus-cleanup-spec" env:"STORAGE_TUS_CLEANUP_SPEC"`
}

type VideoPipelineConfig struct {
	ResizerHost         string              `toml:"resizer_host" long:"storage-video-resizer-host" env:"STORAGE_VIDEO_RESIZER_HOST" value-default:"http://media_resizer:18085/jobs" validate:"required"`
	WebhookCallbackHost string              `toml:"webhook_host" long:"storage-video-resizer-webhook-host" env:"STORAGE_VIDEO_RESIZER_WEBHOOK_HOST" value-default:"http://backend:8080/api/v1/media-resize" validate:"required"`
	ResizerToken        string              `toml:"resizer_token" long:"storage-video-resizer-token" env:"STORAGE_VIDEO_RESIZER_TOKEN" value-default:""`
	Presets             []VideoPresetConfig `toml:"presets" validate:"required,dive"`
}

type PresetPreviewConfig struct {
	Enabled   bool   `toml:"enabled" value-default:"false"`
	Timestamp int    `toml:"timestamp" validate:"min=0"`
	Width     int    `toml:"width" validate:"min=0"`
	Height    int    `toml:"height" validate:"min=0"`
	Format    string `toml:"format" validate:"omitempty,oneof=jpg jpeg png webp"`
}

type VideoPresetConfig struct {
	Name         string              `toml:"name" validate:"required"`
	Width        int                 `toml:"width" validate:"min=0"`
	Height       int                 `toml:"height" validate:"min=0"`
	Quality      int                 `toml:"quality" value-default:"82" validate:"min=1,max=100"`
	Format       string              `toml:"format" validate:"omitempty,oneof=mp4"`
	VideoBitrate int                 `toml:"video_bitrate"`
	AudioBitrate int                 `toml:"audio_bitrate"`
	Thumbnail    PresetPreviewConfig `toml:"thumbnail"`
	Preview      PresetPreviewConfig `toml:"preview"`
}

type ImagePipelineConfig struct {
	DefaultFormat       string               `toml:"default_format" long:"storage-image-default-format" env:"STORAGE_IMAGE_DEFAULT_FORMAT" value-default:"jpg" validate:"required,oneof=jpg jpeg png webp"`
	ResizerHost         string               `toml:"resizer_host" long:"storage-image-resizer-host" env:"STORAGE_IMAGE_RESIZER_HOST" value-default:"http://media_resizer:18085/jobs" validate:"required"`
	WebhookCallbackHost string               `toml:"webhook_host" long:"storage-image-resizer-webhook-host" env:"STORAGE_IMAGE_RESIZER_WEBHOOK_HOST" value-default:"http://backend:8080/api/v1/media-resize" validate:"required"`
	ResizerToken        string               `toml:"resizer_token" long:"storage-image-resizer-token" env:"STORAGE_IMAGE_RESIZER_TOKEN" value-default:""`
	Presets             []ImagePresetConfig  `toml:"presets" validate:"required,dive"`
	Watermark           ImageWatermarkConfig `toml:"watermark"`
}

type ImagePresetConfig struct {
	Name    string `toml:"name" validate:"required"`
	Width   int    `toml:"width" validate:"min=0"`
	Height  int    `toml:"height" validate:"min=0"`
	Fit     string `toml:"fit" value-default:"cover" validate:"omitempty,oneof=cover contain fill inside outside"`
	Quality int    `toml:"quality" value-default:"82" validate:"min=1,max=100"`
	Format  string `toml:"format" validate:"omitempty,oneof=jpg jpeg png webp"`
}

type ImageWatermarkConfig struct {
	Enabled  bool    `toml:"enabled" value-default:"false"`
	Image    string  `toml:"image"`
	Opacity  float64 `toml:"opacity" value-default:"0.4" validate:"min=0,max=1"`
	Position string  `toml:"position" value-default:"center" validate:"omitempty,oneof=center top-left top-right bottom-left bottom-right"`
	OffsetX  int     `toml:"offset_x" value-default:"0"`
	OffsetY  int     `toml:"offset_y" value-default:"0"`
	Scale    float64 `toml:"scale" value-default:"0" validate:"min=0,max=1"`
}
