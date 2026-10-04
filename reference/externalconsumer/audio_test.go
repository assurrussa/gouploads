package externalconsumer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestAudioPublicContract(t *testing.T) {
	require.EqualValues(t, 7, host.FileTypeAudio)
	require.Equal(t, "audio", host.GetFileType(host.FileTypeAudio))
	require.Equal(t, host.FileTypeAudio, host.GetFileTypeString("audio"))
	for _, mime := range []string{"audio/mpeg", "audio/wav", "audio/x-wav"} {
		kind, err := host.GetFileTypeFromMimeType(mime)
		require.NoError(t, err)
		require.Equal(t, host.FileTypeAudio, kind)
	}
}
