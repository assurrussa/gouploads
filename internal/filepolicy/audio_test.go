package filepolicy_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/audiofixture"
	"github.com/assurrussa/gouploads/internal/filepolicy"
)

func TestAudioContentSniffingAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, mime, extension string
		body                  []byte
	}{
		{"tagged MP3", "audio/mpeg", ".mp3", audiofixture.MP3},
		{"untagged MP3", "audio/mpeg", ".mp3", audiofixture.UntaggedMP3()},
		{"WAV", "audio/wav", ".wav", audiofixture.WAV},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := tc.body[:min(512, len(tc.body))]
			require.Equal(t, tc.mime, filepolicy.DetectContentType(header))
			require.NoError(t, filepolicy.ValidateAudioHeader(header, tc.mime, int64(len(tc.body))))
			require.Equal(t, tc.extension, filepolicy.Extension(tc.mime))
			require.NoError(t, filepolicy.ValidateOriginalConfig(50<<20,
				[]string{tc.extension}, map[string][]string{tc.extension: {tc.mime}}))
		})
	}
	for _, alias := range []string{"audio/wave", "audio/x-wav", "audio/vnd.wave", "AUDIO/WAV; charset=binary"} {
		require.Equal(t, "audio/wav", filepolicy.NormalizeMIME(alias))
		require.Equal(t, ".wav", filepolicy.Extension(alias))
	}
	for _, alias := range []string{"audio/mp3", "audio/x-mp3", "audio/mpeg3", "audio/x-mpeg-3"} {
		require.Equal(t, "audio/mpeg", filepolicy.NormalizeMIME(alias))
	}
	require.Error(t, filepolicy.ValidateOriginalConfig(50<<20, []string{".mp3"}, map[string][]string{".mp3": {"audio/wav"}}))
	require.Empty(t, filepolicy.Extension("audio/ogg"))
	require.Equal(t, "text/plain; charset=utf-8", filepolicy.DetectContentType([]byte("unchanged text")))
}

func TestAudioRejectsTruncatedAndInvalidHeaders(t *testing.T) {
	for _, header := range [][]byte{[]byte("ID3"), []byte("ID3\xff\x00\x00\x00\x00\x00\x00"), {0xff, 0xff, 0xff, 0xff}} {
		require.NotEqual(t, "audio/mpeg", filepolicy.DetectContentType(header))
	}
	require.Error(t, filepolicy.ValidateAudioHeader(audiofixture.MP3[:10], "audio/mpeg", 10))
	require.Error(t, filepolicy.ValidateAudioHeader(audiofixture.UntaggedMP3()[:4], "audio/mpeg", 4))
	require.Error(t, filepolicy.ValidateAudioHeader(audiofixture.UntaggedMP3()[:5], "audio/mpeg", 5))
	require.Error(t, filepolicy.ValidateAudioHeader([]byte("ID3\x03\x00\x00\x00\x00\x00\x00junk"), "audio/mpeg", 14))
	require.Error(t, filepolicy.ValidateAudioHeader(audiofixture.WAV[:12], "audio/wav", 12))
	require.Error(t, filepolicy.ValidateAudioHeader([]byte("not wave"), "audio/wav", 100))
}

func TestLargeID3FrameInspectionIsBoundedAndReplayed(t *testing.T) {
	valid := audiofixture.MP3WithTag(4096)
	invalid := bytes.Clone(valid)
	copy(invalid[4106:], "junk")
	for _, tc := range []struct {
		name  string
		body  []byte
		valid bool
	}{
		{"valid", valid, true},
		{"exact budget", audiofixture.MP3WithTag(int(filepolicy.HeaderBudget) - 14), true},
		{"forged frame", invalid, false},
		{"truncated frame", valid[:4110], false},
		{"over budget", audiofixture.MP3WithTag(int(filepolicy.HeaderBudget)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := tc.body[:512]
			require.Error(t, filepolicy.ValidateAudioHeader(header, "audio/mpeg", int64(len(tc.body))))
			counter := &countReader{reader: bytes.NewReader(tc.body)}
			replay, err := filepolicy.InspectAudioHeader(counter, header, "audio/mpeg", int64(len(tc.body)))
			require.LessOrEqual(t, counter.n, filepolicy.HeaderBudget)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got, err := io.ReadAll(replay)
			require.NoError(t, err)
			require.Equal(t, tc.body, got)
		})
	}
}

func TestM4AContainerDetectionAndCompatibility(t *testing.T) {
	header := audiofixture.M4A[:min(512, len(audiofixture.M4A))]
	require.Equal(t, "audio/mp4", filepolicy.DetectContentTypeForName(header, "урок.M4A", []string{"audio/mp4"}))
	require.NotEqual(t, "audio/mp4", filepolicy.DetectContentTypeForName(header, "урок.mp4", []string{"audio/mp4"}))
	require.NoError(t, filepolicy.ValidateAudioHeader(header, "audio/mp4", int64(len(audiofixture.M4A))))
	require.Equal(t, ".m4a", filepolicy.Extension("audio/x-m4a"))
	require.NoError(t, filepolicy.ValidateOriginalConfig(50<<20, []string{".m4a"}, map[string][]string{".m4a": {"audio/mp4"}}))
	require.Error(t, filepolicy.ValidateOriginalConfig(50<<20, []string{".m4a"}, map[string][]string{".m4a": {"video/mp4"}}))
	for _, alias := range []string{"audio/x-m4a", "audio/m4a", "audio/x-mp4"} {
		require.Equal(t, "audio/mp4", filepolicy.NormalizeMIME(alias))
	}
	for _, prefix := range [][]byte{
		[]byte("not an MP4"), audiofixture.M4A[:12],
		[]byte("\x00\x00\x00\x10ftypzzzz\x00\x00\x00\x00"),
		[]byte("\x00\x00\x00\x08ftypM4A "),
	} {
		require.NotEqual(t, "audio/mp4", filepolicy.DetectContentTypeForName(prefix, "audio.m4a", []string{"audio/mp4"}))
		require.Error(t, filepolicy.ValidateAudioHeader(prefix, "audio/mp4", int64(len(prefix))))
	}
	// Files using generic MP4 compatible brands are audio only when the host
	// explicitly opts into .m4a; existing video/mp4 detection is unchanged.
	generic := []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00isommp42")
	require.Equal(t, "audio/mp4", filepolicy.DetectContentTypeForName(generic, "audio.m4a", []string{"audio/mp4"}))
	require.Equal(t, "video/mp4", filepolicy.DetectContentTypeForName(generic, "video.mp4", []string{"video/mp4"}))
	require.Equal(t, "video/mp4", filepolicy.DetectContentTypeForName(generic, "legacy.m4a", []string{"video/mp4"}))
	require.Equal(t, "video/mp4", filepolicy.DetectContentTypeForName(generic, "legacy.m4a", []string{"video/*", "audio/mp4"}))
	require.Equal(t, "video/mp4", filepolicy.DetectContentTypeForName(generic, "legacy.m4a", nil))
	require.Error(t, filepolicy.ValidateAudioHeader(header, "audio/mp4", 12))
}
