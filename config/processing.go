package config

import (
	"errors"
	"fmt"
	"strings"
)

// NormalizeProcessingConfig validates the processing boundary independently
// from object-storage normalization. Storage-only clients need no resizer.
func NormalizeProcessingConfig(cfg StorageConfig) (StorageConfig, error) {
	mode, err := cfg.ProcessingMode.Resolve()
	if err != nil {
		return StorageConfig{}, err
	}
	cfg.ProcessingMode = mode
	if mode == ProcessingOriginalOnly {
		if len(cfg.Image.Presets) != 0 || len(cfg.Video.Presets) != 0 || cfg.Image.Watermark.Enabled {
			return StorageConfig{}, fmt.Errorf("%w: presets/watermarks require media_resizer", ErrProcessingDisabled)
		}
		return cfg, nil
	}

	// The existing media pipeline supports both image and video dispatch. Do not
	// infer enablement from an endpoint or invent service-discovery defaults.
	values := []struct {
		name  string
		value *string
	}{
		{"image resizer URL", &cfg.Image.ResizerHost},
		{"image callback URL", &cfg.Image.WebhookCallbackHost},
		{"video resizer URL", &cfg.Video.ResizerHost},
		{"video callback URL", &cfg.Video.WebhookCallbackHost},
	}
	for _, value := range values {
		*value.value, err = normalizeAbsoluteHTTPURL(value.name, *value.value)
		if err != nil {
			return StorageConfig{}, err
		}
	}
	if strings.TrimSpace(cfg.Image.ResizerToken) == "" || strings.TrimSpace(cfg.Video.ResizerToken) == "" {
		return StorageConfig{}, errors.New("media_resizer requires explicit image and video API tokens")
	}
	if cfg.Image.DefaultFormat == "" {
		cfg.Image.DefaultFormat = "jpg"
	}
	return cfg, nil
}
