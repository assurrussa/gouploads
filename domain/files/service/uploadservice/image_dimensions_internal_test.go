package uploadservice

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
)

func TestInspectImageDimensionsPreservesReader(t *testing.T) {
	body, err := io.ReadAll(testshelpers.CreateTestImage(t))
	require.NoError(t, err)

	replay, width, height := inspectImageDimensions(bytes.NewReader(body), "image/png")
	require.Equal(t, 2, width)
	require.Equal(t, 2, height)

	got, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestInspectImageDimensionsPreservesInvalidImageReader(t *testing.T) {
	body := []byte("not an image")

	replay, width, height := inspectImageDimensions(bytes.NewReader(body), "image/png")
	require.Zero(t, width)
	require.Zero(t, height)

	got, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, body, got)
}
