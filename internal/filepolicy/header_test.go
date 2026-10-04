package filepolicy_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/filepolicy"
)

func TestIncompleteHeadersRemainPrivateUntilSignatureIsComplete(t *testing.T) {
	for _, header := range []struct {
		contentType string
		signature   string
	}{
		{"image/png", "\x89PNG\r\n\x1a\n"},
		{"image/jpeg", "\xff\xd8\xff"},
		{"image/gif", "GIF89a"},
		{"application/pdf", "%PDF-"},
		{"image/webp", "RIFF\x18\x00\x00\x00WEBPVP"},
		{"audio/mpeg", "ID3\x03\x00\x00\x00\x00\x00\x00"},
		{"audio/mpeg", "\xff\xfb\x90\x00"},
		{"audio/wav", "RIFF\x18\x00\x00\x00WAVE"},
		{"video/webm", "\x1a\x45\xdf\xa3"},
		{"video/mp4", "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00isommp42"},
	} {
		t.Run(header.contentType, func(t *testing.T) {
			for size := 1; size < len(header.signature); size++ {
				require.True(t, filepolicy.IncompleteMIMEHeader([]byte(header.signature[:size]), header.contentType))
			}
			require.False(t, filepolicy.IncompleteMIMEHeader([]byte(header.signature), header.contentType))
			require.False(t, filepolicy.IncompleteMIMEHeader([]byte("malformed"), header.contentType))
		})
	}
}
