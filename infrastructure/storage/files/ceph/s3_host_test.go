package ceph_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
)

func TestDomainHost_Complex(t *testing.T) {
	domain := ceph.NewDomainHost("https://test.app", "bucket-key", "public-read", true, false, false)
	assert.Equal(t, "https://test.app/", domain.Host())
	assert.Equal(t, "https://bucket-key.test.app/", domain.String())
	assert.Equal(t, "bucket-key", domain.Bucket())
	assert.Equal(t, "public-read", domain.ACL())

	domain = ceph.NewDomainHost("https://test.app", "bucket-key", "public-read", true, true, false)
	assert.Equal(t, "http://bucket-key.test.app/", domain.String())

	domain = ceph.NewDomainHost("https://test.app", "bucket-key", "public-read", true, false, true)
	assert.Equal(t, "https://test.app/", domain.String())

	domain = ceph.NewDomainHost("https://test.app", "bucket-key", "public-read", true, true, true)
	assert.Equal(t, "http://test.app/", domain.String())

	domain = ceph.NewDomainHost("http://test.app", "bucket-key", "public-read", true, false, true)
	assert.Equal(t, "https://test.app/", domain.String())

	domain = ceph.NewDomainHost("http://test.app", "bucket-key", "public-read", true, true, false)
	assert.Equal(t, "http://bucket-key.test.app/", domain.String())

	domain = ceph.NewDomainHost("http://test.app", "bucket-key", "public-read", true, false, false)
	assert.Equal(t, "https://bucket-key.test.app/", domain.String())

	domain = ceph.NewDomainHost("https://test.app", "bucket-key", "public-read", true, true, true)
	assert.Equal(t, "http://test.app/", domain.String())

	domain = ceph.NewDomainHost("https://test.app/", "bucket-key", "public-read", false, true, true)
	assert.Equal(t, "http://test.app/", domain.String())
}
