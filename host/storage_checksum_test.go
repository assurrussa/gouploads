package host_test

import (
	"context"
	"crypto/md5" //nolint:gosec // Required S3 wire checksum, not a security primitive.
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

type capturedDelete struct {
	body   []byte
	header http.Header
	path   string
}

func TestStorageDeleteChecksumAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		keys     []string
		retry    bool
		requests int
	}{
		{"single", []string{"media/v1/post/42/a & b.pdf"}, false, 1},
		{"batch_boundary", deletionKeys(1001), false, 2},
		{"retry", []string{"staging/v1/tus/retry/source.pdf"}, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var captured []capturedDelete
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				mu.Lock()
				captured = append(captured, capturedDelete{body: body, header: r.Header.Clone(), path: r.URL.Path})
				attempt := len(captured)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/xml")
				if tc.retry && attempt == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `<Error><Code>SlowDown</Code><Message>retry fixture</Message></Error>`)
					return
				}
				_, _ = io.WriteString(w, `<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"></DeleteResult>`)
			}))
			defer server.Close()
			storage, err := host.NewStorage(host.StorageConfig{
				Driver: host.StorageDriverS3,
				Public: host.StoragePublicConfig{BaseURL: server.URL + "/public"},
				S3: host.StorageS3Config{
					Endpoint: server.URL, Region: "us-east-1", Bucket: "public", StagingBucket: "staging",
					AccessKey: "fixture", SecretKey: "fixture", ForcePathStyle: true, MaxRetries: 1,
				},
			})
			require.NoError(t, err)
			require.NoError(t, storage.DeleteBatch(context.Background(), tc.keys))
			mu.Lock()
			requests := append([]capturedDelete(nil), captured...)
			mu.Unlock()
			require.Len(t, requests, tc.requests)
			actualKeys := make([]string, 0, len(tc.keys)*tc.requests)
			for _, request := range requests {
				requireDeleteChecksums(t, request)
				var payload struct {
					Objects []struct {
						Key string `xml:"Key"`
					} `xml:"Object"`
				}
				require.NoError(t, xml.Unmarshal(request.body, &payload))
				require.LessOrEqual(t, len(payload.Objects), 1000)
				for _, object := range payload.Objects {
					actualKeys = append(actualKeys, object.Key)
				}
			}
			if tc.retry {
				require.Equal(t, requests[0].body, requests[1].body)
				require.Equal(t, requests[0].header.Get("Content-MD5"), requests[1].header.Get("Content-MD5"))
				require.Equal(t, "/staging", requests[0].path)
				require.Equal(t, append(append([]string(nil), tc.keys...), tc.keys...), actualKeys)
			} else {
				require.Equal(t, tc.keys, actualKeys)
			}
		})
	}
}

func deletionKeys(count int) []string {
	keys := make([]string, count)
	for index := range count {
		keys[index] = fmt.Sprintf("media/v1/post/42/file-%04d.pdf", index)
	}
	return keys
}

func requireDeleteChecksums(t *testing.T, request capturedDelete) {
	t.Helper()
	sum := md5.Sum(request.body) //nolint:gosec // Required S3 wire checksum, not a security primitive.
	require.Equal(t, base64.StdEncoding.EncodeToString(sum[:]), request.header.Get("Content-MD5"))
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(request.body))
	require.Equal(t, base64.StdEncoding.EncodeToString(crc[:]), request.header.Get("X-Amz-Checksum-Crc32"))
	require.Contains(t, request.header.Get("Authorization"), "content-md5")
}
