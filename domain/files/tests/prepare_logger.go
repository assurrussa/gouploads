//go:build integration

package testshelpers

import (
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
)

func CreateLogger(t *testing.T) *logger.Log {
	t.Helper()

	log, err := logger.NewLogger(logger.Config{
		Env:       "production",
		Level:     "info",
		JSON:      true,
		Rate:      0.0,
		AddSource: false,
	})
	if err != nil {
		t.Fatal("logger", logger.Error(err))
	}

	return log
}
