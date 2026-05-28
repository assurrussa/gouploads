package hosttest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
)

func TestListenResizeRequestMatcherMatchesNumericMetadataAcrossJSONTypes(t *testing.T) {
	t.Parallel()

	expected := host.ListenResizeRequest{
		ExternalID: 42,
		Status:     "done",
		Metadata: map[string]any{
			"fileId": 42,
		},
		Artifacts: []host.ResizeArtifact{{
			Preset: "main",
			URL:    "https://example.test/file.webp",
			Metadata: map[string]any{
				"source_bytes": 812957,
			},
		}},
	}
	got := expected
	got.Metadata = map[string]any{"fileId": float64(42)}
	got.Artifacts = []host.ResizeArtifact{{
		Preset: "main",
		URL:    "https://example.test/file.webp",
		Metadata: map[string]any{
			"source_bytes": float64(812957),
		},
	}}

	matcher := hosttest.NewListenResizeRequestMatcher("resize callback", expected)
	require.True(t, matcher.Matches(got), matcher.String())
}

func TestListenResizeRequestMatcherRejectsDifferentArtifact(t *testing.T) {
	t.Parallel()

	expected := host.ListenResizeRequest{
		ExternalID: 42,
		Status:     "done",
		Artifacts: []host.ResizeArtifact{{
			Preset: "main",
			URL:    "https://example.test/file.webp",
		}},
	}
	got := host.ListenResizeRequest{
		ExternalID: 42,
		Status:     "done",
		Artifacts: []host.ResizeArtifact{{
			Preset: "main",
			URL:    "https://example.test/other.webp",
		}},
	}

	matcher := hosttest.NewListenResizeRequestMatcher("resize callback", expected)
	require.False(t, matcher.Matches(got))
}
