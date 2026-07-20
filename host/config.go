package host

import uploadconfig "github.com/assurrussa/gouploads/config"

const (
	StorageDriverLocal = uploadconfig.StorageDriverLocal
	StorageDriverS3    = uploadconfig.StorageDriverS3
)

type (
	ImagePipelineConfig  = uploadconfig.ImagePipelineConfig
	ImagePresetConfig    = uploadconfig.ImagePresetConfig
	ImageWatermarkConfig = uploadconfig.ImageWatermarkConfig
	ParseSize            = uploadconfig.ParseSize
	PresetPreviewConfig  = uploadconfig.PresetPreviewConfig
	StorageConfig        = uploadconfig.StorageConfig
	StorageLocalConfig   = uploadconfig.StorageLocalConfig
	StoragePublicConfig  = uploadconfig.StoragePublicConfig
	StorageS3Config      = uploadconfig.StorageS3Config
	StorageTusConfig     = uploadconfig.StorageTusConfig
	VideoPipelineConfig  = uploadconfig.VideoPipelineConfig
	VideoPresetConfig    = uploadconfig.VideoPresetConfig
)
