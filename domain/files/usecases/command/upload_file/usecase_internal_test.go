package uploadfile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactPresetFileNameUsesOnlyPresetAndMIME(t *testing.T) {
	name, err := artifactPresetFileName(Artifact{
		Preset: "card",
		URL:    "https://resizer.example.com/temp/original-name.png?signature=secret",
	}, "image/webp")

	require.NoError(t, err)
	require.Equal(t, "card.webp", name)
}

func TestArtifactPresetFileNameRejectsUnknownExtension(t *testing.T) {
	name, err := artifactPresetFileName(Artifact{Preset: "main"}, "invalid/no-extension")

	require.Empty(t, name)
	require.ErrorContains(t, err, "no supported extension")
}

func TestValidateArtifactContentTypeUsesNarrowAllowlist(t *testing.T) {
	selected, err := validateArtifactContentType("application/pdf", "application/pdf", "application/pdf")
	require.NoError(t, err)
	require.Equal(t, "application/pdf", selected)

	_, err = validateArtifactContentType("image/svg+xml", "image/svg+xml", "image/svg+xml")
	require.ErrorContains(t, err, "not allowed")
}
