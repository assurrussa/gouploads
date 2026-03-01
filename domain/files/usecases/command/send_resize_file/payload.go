package sendresizefile

import (
	"fmt"

	"github.com/goccy/go-json"
)

type Payload struct {
	IdempotencyKey   string            `json:"idempotency_key"` //nolint:tagliatelle // external client
	Type             string            `json:"type"`
	SkipResize       bool              `json:"skip_resize"`        //nolint:tagliatelle // external client
	NotifyWebhookURL string            `json:"notify_webhook_url"` //nolint:tagliatelle // external client
	Metadata         map[string]string `json:"metadata"`
	Presets          []Preset          `json:"presets"`
	Source           Source            `json:"source"`
}

type Preset struct {
	Format       string         `json:"format"`
	Height       int            `json:"height"`
	Name         string         `json:"name"`
	Target       string         `json:"target"`
	Fit          string         `json:"fit"`
	Width        int            `json:"width"`
	Quality      int            `json:"quality"`
	VideoBitrate int            `json:"video_bitrate"` //nolint:tagliatelle // external client
	AudioBitrate int            `json:"audio_bitrate"` //nolint:tagliatelle // external client
	Thumbnail    *PresetPreview `json:"thumbnail,omitempty"`
	Preview      *PresetPreview `json:"preview,omitempty"`
}

type PresetPreview struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Format    string `json:"format,omitempty"`
}
type Source struct {
	URL string `json:"url"`
}

func NewPayload(
	idempotencyKey string,
	typePreset string,
	skipResize bool,
	notifyWebhookURL string,
	presets []Preset,
	source Source,
	metadata ...map[string]string,
) Payload {
	var m map[string]string
	for _, meta := range metadata {
		m = meta
		break
	}

	return Payload{
		IdempotencyKey:   idempotencyKey,
		Type:             typePreset,
		SkipResize:       skipResize,
		NotifyWebhookURL: notifyWebhookURL,
		Metadata:         m,
		Presets:          presets,
		Source:           source,
	}
}

func MarshalPayload(p Payload) ([]byte, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	return b, nil
}

func UnmarshalPayload(data string) (Payload, error) {
	var p Payload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return Payload{}, fmt.Errorf("unmarshal payload: %w", err)
	}
	return p, nil
}
