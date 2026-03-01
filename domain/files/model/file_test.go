package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/assurrussa/gouploads/domain/files/model"
	filesshared "github.com/assurrussa/gouploads/domain/files/shared"
)

func TestPreferredPreviewPath(t *testing.T) {
	tests := []struct {
		name     string
		file     model.File
		expected string
	}{
		{
			name: "preview preset absolute url",
			file: model.File{
				MimeType: "video/mp4",
				Data: &model.FileData{
					Presets: map[filesshared.PresetName]filesshared.FilePreset{
						"video_preview": {
							PresetName: "video_preview",
							URL:        "https://cdn.example.com/previews/video.mp4",
							IsPreview:  true,
						},
					},
				},
			},
			expected: "https://cdn.example.com/previews/video.mp4",
		},
		{
			name: "preview preset relative path",
			file: model.File{
				MimeType: "video/mp4",
				Data: &model.FileData{
					Presets: map[filesshared.PresetName]filesshared.FilePreset{
						"video_preview": {
							PresetName:   "video_preview",
							RelativePath: "uploads/videos/previews/video.mp4",
							IsPreview:    true,
						},
					},
				},
			},
			expected: "uploads/videos/previews/video.mp4",
		},
		{
			name: "thumbnail preset when preview missing",
			file: model.File{
				MimeType: "video/mp4",
				Data: &model.FileData{
					Presets: map[filesshared.PresetName]filesshared.FilePreset{
						"video_thumb": {
							PresetName:  "video_thumb",
							URL:         "https://cdn.example.com/thumbs/video.webp",
							IsThumbnail: true,
						},
					},
				},
			},
			expected: "https://cdn.example.com/thumbs/video.webp",
		},
		{
			name: "image fallback to thumbnail path",
			file: model.File{
				MimeType:   "image/png",
				FileName:   "picture.png",
				FolderPath: "uploads/images",
			},
			expected: "/uploads/images/picture.png",
		},
		{
			name: "non video fallback to public url",
			file: model.File{
				MimeType: "application/pdf",
				URL:      "https://cdn.example.com/docs/doc.pdf",
			},
			expected: "https://cdn.example.com/docs/doc.pdf",
		},
		{
			name: "video without preview returns empty",
			file: model.File{
				MimeType: "video/mp4",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.file.PreferredPreviewPath())
		})
	}
}
