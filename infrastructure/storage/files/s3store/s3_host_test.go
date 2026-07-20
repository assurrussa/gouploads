package s3store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
)

func TestDomainHostRoutesStagingAndPublicKeys(t *testing.T) {
	domain := s3store.NewDomainHost(
		"https://media.example.test/",
		"public-bucket",
		"staging-bucket",
		"staging/v1/tus",
	)

	assert.Equal(t, "https://media.example.test", domain.Host())
	assert.Equal(t, "https://media.example.test", domain.String())
	assert.Equal(t, "public-bucket", domain.Bucket())
	assert.Equal(t, "staging-bucket", domain.StagingBucket())
	assert.Equal(t, "staging-bucket", domain.BucketForKey("staging/v1/tus/session/source.jpg"))
	assert.Equal(t, "public-bucket", domain.BucketForKey("media/v1/post/1/file/main.jpg"))
}

func TestDomainHostDefaultsStagingBucketToPublicBucket(t *testing.T) {
	domain := s3store.NewDomainHost("https://media.example.test", "media", "", "staging/v1/tus")

	assert.Equal(t, "media", domain.StagingBucket())
}
