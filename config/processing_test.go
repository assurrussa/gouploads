package config

import (
	"errors"
	"testing"
)

func TestNormalizeProcessingOriginalDefault(t *testing.T) {
	cfg, err := NormalizeProcessingConfig(StorageConfig{})
	if err != nil || cfg.ProcessingMode != ProcessingOriginalOnly {
		t.Fatalf("default processing: mode=%q err=%v", cfg.ProcessingMode, err)
	}
	// Merely retaining an endpoint while draining old jobs must not enable media.
	cfg.Image.ResizerHost = "http://processor.internal/jobs"
	cfg, err = NormalizeProcessingConfig(cfg)
	if err != nil || cfg.ProcessingMode != ProcessingOriginalOnly {
		t.Fatalf("endpoint enabled processing: %v", err)
	}
}

func TestOriginalRejectsPresetsAndWatermarks(t *testing.T) {
	for _, cfg := range []StorageConfig{
		{Image: ImagePipelineConfig{Presets: []ImagePresetConfig{{Name: "thumb"}}}},
		{Video: VideoPipelineConfig{Presets: []VideoPresetConfig{{Name: "preview"}}}},
		{Image: ImagePipelineConfig{Watermark: ImageWatermarkConfig{Enabled: true}}},
	} {
		if _, err := NormalizeProcessingConfig(cfg); !errors.Is(err, ErrProcessingDisabled) {
			t.Fatalf("expected disabled processing, got %v", err)
		}
	}
}

func TestMediaProcessingRequiresExplicitConfig(t *testing.T) {
	cfg := StorageConfig{ProcessingMode: ProcessingMediaResizer}
	if _, err := NormalizeProcessingConfig(cfg); err == nil {
		t.Fatal("unconfigured resizer accepted")
	}
	cfg.Image.ResizerHost = "https://processor.example.test/jobs"
	cfg.Image.WebhookCallbackHost = "https://app.example.test/internal/media"
	cfg.Video.ResizerHost = cfg.Image.ResizerHost
	cfg.Video.WebhookCallbackHost = cfg.Image.WebhookCallbackHost
	if _, err := NormalizeProcessingConfig(cfg); err == nil {
		t.Fatal("missing API tokens accepted")
	}
	cfg.Image.ResizerToken = "test-token"
	cfg.Video.ResizerToken = "test-token"
	resolved, err := NormalizeProcessingConfig(cfg)
	if err != nil || resolved.Image.DefaultFormat != "jpg" {
		t.Fatalf("explicit media configuration: %v", err)
	}
	if cfg.Image.DefaultFormat != "" {
		t.Fatal("normalization mutated the input")
	}
	cfg.Video.WebhookCallbackHost = "file:///tmp/callback"
	if _, err := NormalizeProcessingConfig(cfg); err == nil {
		t.Fatal("non-HTTP callback accepted")
	}
}
