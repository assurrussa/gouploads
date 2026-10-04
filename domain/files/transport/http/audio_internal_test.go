package http

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAudioMIMEAliasesMatchDuringTUS(t *testing.T) {
	for _, alias := range []string{"audio/wav", "audio/wave", "audio/x-wav", "audio/vnd.wave"} {
		require.True(t, isAllowedMimeType(".wav", "audio/wav", map[string][]string{".wav": {alias}}))
		require.False(t, isAllowedMimeType(".mp3", "audio/wav", map[string][]string{".mp3": {"audio/mpeg"}}))
	}
	require.True(t, isAllowedMimeType(".mp3", "audio/mpeg", map[string][]string{".mp3": {"audio/mp3"}}))
}
