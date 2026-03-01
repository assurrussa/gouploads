package ceph

import "strings"

type DomainHost struct {
	host        string
	bucket      string
	acl         string
	transform   bool
	disableSSL  bool
	isForcePath bool
}

func NewDomainHost(host string, bucket string, acl string, transform bool, disableSSL bool, isForcePath bool) DomainHost {
	if disableSSL && strings.HasPrefix(host, "https://") {
		host = strings.Replace(host, "https://", "http://", 1)
	}

	if !disableSSL && strings.HasPrefix(host, "http://") {
		host = strings.Replace(host, "http://", "https://", 1)
	}

	if !strings.HasSuffix(host, "/") {
		host += "/"
	}

	return DomainHost{
		host:        host,
		bucket:      bucket,
		acl:         acl,
		transform:   transform,
		disableSSL:  disableSSL,
		isForcePath: isForcePath,
	}
}

func (d DomainHost) String() string {
	if !d.transform {
		return d.Host()
	}

	if d.isForcePath {
		return d.Host()
	}

	return strings.Replace(d.host, "://", "://"+d.Bucket()+".", 1)
}

func (d DomainHost) Host() string {
	return d.host
}

func (d DomainHost) Bucket() string {
	return d.bucket
}

func (d DomainHost) ACL() string {
	return d.acl
}
