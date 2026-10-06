package model_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
)

func TestFileMIMEPredicates(t *testing.T) {
	for _, tt := range []struct {
		mime         string
		image, video bool
	}{
		{"", false, false},
		{"x", false, false},
		{"im", false, false},
		{"ima", false, false},
		{"imag", false, false},
		{"image", false, false},
		{"v", false, false},
		{"video", false, false},
		{"audio/mpeg", false, false},
		{"image/", true, false},
		{"image/png", true, false},
		{"image/svg+xml", true, false},
		{"video/", false, true},
		{"video/mp4", false, true},
		{"IMAGE/PNG", false, false},
	} {
		t.Run(tt.mime, func(t *testing.T) {
			file := model.File{MimeType: tt.mime}
			require.Equal(t, tt.image, file.IsImage())
			require.Equal(t, tt.video, file.IsVideo())
		})
	}
}

func FuzzFileMIMEPredicates(f *testing.F) {
	for _, mime := range []string{"", "x", "image", "video", "image/png", "image/svg+xml", "video/mp4", "\x00\xff"} {
		f.Add(mime)
	}
	f.Fuzz(func(t *testing.T, mime string) {
		file := model.File{MimeType: mime}
		require.Equal(t, strings.HasPrefix(mime, "image/"), file.IsImage())
		require.Equal(t, strings.HasPrefix(mime, "video/"), file.IsVideo())
	})
}
