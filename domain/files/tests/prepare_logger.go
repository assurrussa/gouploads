//go:build integration

package testshelpers

import (
	"testing"

	logger "github.com/assurrussa/gologger"
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
