package host_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestNewSlogLogger_Nil(t *testing.T) {
	t.Parallel()

	lg := host.NewSlogLogger(nil)
	require.NotNil(t, lg)

	// Discard logger must not panic on standard logging calls
	lg.InfoContext(context.Background(), "test message")
	lg.ErrorContext(context.Background(), "test error")
}

func TestNewSlogLogger_CustomSlog(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	sl := slog.New(slog.NewJSONHandler(&buf, nil))

	lg := host.NewSlogLogger(sl)
	require.NotNil(t, lg)

	lg.InfoContext(context.Background(), "hello slog from gouploads")
	require.Contains(t, buf.String(), "hello slog from gouploads")
}
