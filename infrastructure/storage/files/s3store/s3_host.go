package s3store

import "strings"

type DomainHost struct {
	publicBaseURL string
	bucket        string
	stagingBucket string
	stagingPrefix string
}

func NewDomainHost(publicBaseURL, bucket, stagingBucket, stagingPrefix string) DomainHost {
	bucket = strings.Trim(bucket, "/")
	stagingBucket = strings.Trim(stagingBucket, "/")
	if stagingBucket == "" {
		stagingBucket = bucket
	}

	return DomainHost{
		publicBaseURL: strings.TrimRight(strings.TrimSpace(publicBaseURL), "/"),
		bucket:        bucket,
		stagingBucket: stagingBucket,
		stagingPrefix: strings.Trim(stagingPrefix, "/"),
	}
}

func (d DomainHost) String() string {
	return d.publicBaseURL
}

func (d DomainHost) Host() string {
	return d.publicBaseURL
}

func (d DomainHost) Bucket() string {
	return d.bucket
}

func (d DomainHost) StagingBucket() string {
	return d.stagingBucket
}

func (d DomainHost) StagingPrefix() string {
	return d.stagingPrefix
}

func (d DomainHost) IsStagingKey(key string) bool {
	key = strings.TrimLeft(key, "/")
	return d.stagingPrefix != "" && (key == d.stagingPrefix || strings.HasPrefix(key, d.stagingPrefix+"/"))
}

func (d DomainHost) BucketForKey(key string) string {
	if d.IsStagingKey(key) {
		return d.stagingBucket
	}

	return d.bucket
}
