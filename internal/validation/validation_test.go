package validation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/validation"
)

func TestSizeValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"1", "1KB", "2MB"} {
		require.NoError(t, validation.Validator.Var(value, "parse-size"))
	}
	for _, value := range []string{"0", "0MB", "bad", "18446744073709551616GB"} {
		require.Error(t, validation.Validator.Var(value, "parse-size"))
	}
}
