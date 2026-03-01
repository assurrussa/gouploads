package uploadfile

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArtifactFileName_ReusesFallbackUUID(t *testing.T) {
	base := uuid.NewString()
	name := artifactFileName(Artifact{
		ContentType: "image/webp",
	}, base+".png", "")

	require.Equal(t, base+".webp", name)
}

func TestArtifactFileName_GeneratesUUIDWhenMissing(t *testing.T) {
	name := artifactFileName(Artifact{
		URL:         "https://resizer.example.com/temp/1.png",
		ContentType: "image/webp",
	}, "example.mp4", "")

	require.True(t, strings.HasSuffix(name, ".webp"))
	require.NotEqual(t, "example.mp4", name)
	base := strings.TrimSuffix(name, ".webp")
	_, err := uuid.Parse(base)
	require.NoError(t, err)
}
