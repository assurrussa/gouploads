# S3 delete compatibility and test stand

`DeleteObjects` includes Content-MD5 computed over the actual serialized XML.
This is an operation-local compatibility addition for providers requiring the
legacy header. The SDK's normal CRC32 integrity and SigV4 signing remain enabled;
upload/download checksum settings are unchanged. MD5 here is protocol integrity
metadata, not a security primitive or a replacement for TLS/signing.

AWS SDK for Go v2 changed checksum-required operations from MD5 to CRC32 starting
with S3 v1.73.0. Amazon S3 accepts alternatives, but older compatible providers
can require Content-MD5. See the [AWS SDK announcement](https://github.com/aws/aws-sdk-go-v2/discussions/2960)
and the [DeleteObjects contract](https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObjects.html).
The added wire regression exercises XML escaping, batch boundaries and retry
replay, checking both checksums and the signed MD5 header.

## Isolated test service pin

The current stand's exact source pin and Go module checksums are in
[`integration/test-services/minio-source.json`](../integration/test-services/minio-source.json).
As verified on 2026-10-06, [RELEASE.2025-10-15T17-29-55Z](https://github.com/minio/minio/releases/tag/RELEASE.2025-10-15T17-29-55Z)
is the latest official non-prerelease Community source release. The repository is archived and no longer
maintained; this is not a claim of active support or absolute stability. Official
binary/image availability must be checked rather than assuming a mutable latest
image. The release documents a source build.

Resolve the exact module version through the Go proxy/checksum database, verify
its recorded source commit and checksums, and build its returned source directory
using Go 1.24 or later. A module-cache build uses `-buildvcs=false`; preserve source
identity and the resulting binary hash separately. Use only disposable loopback
storage/PostgreSQL, fixture credentials and bounded resources. This pin applies
only to the test stand and does not upgrade a production service.

This release accepts both CRC32 and Content-MD5. Consequently, latest-provider
acceptance alone does not prove legacy compatibility: retain the MD5 wire tests
and repeat the formerly failing portable gate against the previous official
RELEASE.2024-05-10T01-41-38Z when checking that regression. The standalone Compose
example's historical image pin is separate from this source-built test stand.

Run the existing real service gates without skips:

- `make test-source-url-s3-integration`
- `make test-portable-s3-media-e2e`
- `make test-originals-integration` (real PostgreSQL, local and S3 original/audio paths)

Record the exact candidate commit/tree, service source/version, commands and
results. A wire recorder is useful for regression isolation and never substitutes
for these real S3 gates.
