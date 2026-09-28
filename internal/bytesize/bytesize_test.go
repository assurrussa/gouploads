package bytesize_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/bytesize"
)

func TestParse(t *testing.T) {
	t.Parallel()
	for _, unit := range []string{"", "B", "b", "KB", "Kb", "kb", "MB", "Mb", "mb", "GB", "Gb", "gb"} {
		multiplier := 1
		if len(unit) == 2 {
			switch unit[0] {
			case 'K', 'k':
				multiplier = bytesize.KB
			case 'M', 'm':
				multiplier = bytesize.MB
			case 'G', 'g':
				multiplier = bytesize.GB
			}
		}
		got, err := bytesize.Parse("2" + unit)
		require.NoError(t, err)
		require.Equal(t, 2*multiplier, got)
	}
	for _, value := range []string{
		"", "-1", " 1MB", "1.5MB", "1kB", "1TB", "18446744073709551616",
		strconv.Itoa(int(^uint(0)>>1)) + "KB",
	} {
		_, err := bytesize.Parse(value)
		require.ErrorIs(t, err, bytesize.ErrIncorrectSize, value)
	}
	value, err := bytesize.Parse(strconv.Itoa(int(^uint(0) >> 1)))
	require.NoError(t, err)
	require.Equal(t, int(^uint(0)>>1), value)
}
