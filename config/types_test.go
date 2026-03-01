package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/assurrussa/gouploads/config"
)

func TestParseSize_Value(t *testing.T) {
	pSizeB := config.ParseSize("1")
	assert.Equal(t, "1", string(pSizeB))
	assert.Equal(t, "1", pSizeB.String())
	assert.Equal(t, 1, pSizeB.Value())

	pSizeKB := config.ParseSize("1KB")
	assert.Equal(t, "1KB", string(pSizeKB))
	assert.Equal(t, "1KB", pSizeKB.String())
	assert.Equal(t, 1024, pSizeKB.Value())
	assert.Equal(t, 1<<10, pSizeKB.Value())

	pSizeMB := config.ParseSize("1MB")
	assert.Equal(t, "1MB", string(pSizeMB))
	assert.Equal(t, "1MB", pSizeMB.String())
	assert.Equal(t, 1048576, pSizeMB.Value())
	assert.Equal(t, 1<<20, pSizeMB.Value())

	pSizeGB := config.ParseSize("1GB")
	assert.Equal(t, "1GB", string(pSizeGB))
	assert.Equal(t, "1GB", pSizeGB.String())
	assert.Equal(t, 1073741824, pSizeGB.Value())
	assert.Equal(t, 1<<30, pSizeGB.Value())

	pSizeInvalid := config.ParseSize("asfasfsafasfasfas")
	assert.Equal(t, "asfasfsafasfasfas", string(pSizeInvalid))
	assert.Equal(t, "asfasfsafasfasfas", pSizeInvalid.String())
	assert.Equal(t, 0, pSizeInvalid.Value())
	assert.Equal(t, 0, pSizeInvalid.Value())
}
