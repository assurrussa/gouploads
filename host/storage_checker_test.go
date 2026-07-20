package host_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestStorageContractCheckerExercisesPortableS3Contract(t *testing.T) {
	t.Parallel()

	storage := newS3ContractStub()
	server := httptest.NewServer(storage)
	t.Cleanup(server.Close)

	checker, err := host.NewStorageContractChecker(host.StorageConfig{
		Driver: host.StorageDriverS3,
		Public: host.StoragePublicConfig{
			BaseURL: server.URL + "/public-bucket",
			Prefix:  "media/v1",
		},
		S3: host.StorageS3Config{
			Endpoint:       server.URL,
			Region:         "test-region",
			Bucket:         "public-bucket",
			StagingBucket:  "staging-bucket",
			AccessKey:      "test-access-key",
			SecretKey:      "test-secret-key",
			ForcePathStyle: true,
			SourceURLTTL:   15 * time.Minute,
			Timeout:        5 * time.Second,
			MaxRetries:     1,
		},
		Tus: host.StorageTusConfig{StagingPrefix: "staging/v1/tus"},
	})
	require.NoError(t, err)

	report, err := checker.Check(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, report.ProbeID)
	require.Equal(t, "public-bucket", report.PublicBucket)
	require.Equal(t, "staging-bucket", report.StagingBucket)
	require.Len(t, report.Checks, 10)
	for _, check := range report.Checks {
		require.Truef(t, check.Success, "%s: %s", check.Name, check.Detail)
	}
	require.Empty(t, report.CleanupErrors)
	require.False(t, report.FinishedAt.Before(report.StartedAt))

	storage.mu.Lock()
	defer storage.mu.Unlock()
	require.Empty(t, storage.objects)
	require.False(t, storage.listAttempted)
}

func TestNewStorageContractCheckerValidatesConfig(t *testing.T) {
	t.Parallel()

	base := host.StorageConfig{
		Driver: host.StorageDriverS3,
		Public: host.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: host.StorageS3Config{
			Endpoint:     "https://s3.example.test",
			Region:       "region-1",
			Bucket:       "media",
			AccessKey:    "access",
			SecretKey:    "secret",
			SourceURLTTL: 15 * time.Minute,
			MaxRetries:   1,
		},
	}

	tests := []struct {
		name   string
		mutate func(*host.StorageConfig)
		want   string
	}{
		{name: "driver", mutate: func(cfg *host.StorageConfig) { cfg.Driver = host.StorageDriverLocal }, want: "requires s3"},
		{name: "endpoint", mutate: func(cfg *host.StorageConfig) { cfg.S3.Endpoint = "s3.example.test" }, want: "HTTP or HTTPS"},
		{name: "public base", mutate: func(cfg *host.StorageConfig) { cfg.Public.BaseURL = "" }, want: "HTTP or HTTPS"},
		{name: "bucket", mutate: func(cfg *host.StorageConfig) { cfg.S3.Bucket = "" }, want: "bucket is required"},
		{name: "ttl", mutate: func(cfg *host.StorageConfig) { cfg.S3.SourceURLTTL = time.Second }, want: "between 1m and 168h"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			test.mutate(&cfg)
			checker, err := host.NewStorageContractChecker(cfg)
			require.Nil(t, checker)
			require.ErrorContains(t, err, test.want)
		})
	}
}

type storedProbeObject struct {
	body         []byte
	contentType  string
	cacheControl string
}

type s3ContractStub struct {
	mu            sync.Mutex
	objects       map[string]storedProbeObject
	listAttempted bool
}

func newS3ContractStub() *s3ContractStub {
	return &s3ContractStub{objects: make(map[string]storedProbeObject)}
}

func (s *s3ContractStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key := splitBucketKey(r.URL.Path)
	if key == "" {
		s.mu.Lock()
		s.listAttempted = true
		s.mu.Unlock()
		http.Error(w, "ListBucket is forbidden", http.StatusForbidden)
		return
	}

	authenticated := r.Header.Get("Authorization") != "" || r.URL.Query().Get("X-Amz-Algorithm") != ""
	objectID := bucket + "/" + key

	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		if !authenticated {
			http.Error(w, "authentication required", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.objects[objectID] = storedProbeObject{
			body:         append([]byte(nil), body...),
			contentType:  r.Header.Get("Content-Type"),
			cacheControl: r.Header.Get("Cache-Control"),
		}
		w.Header().Set("ETag", `"probe-etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		object, ok := s.objects[objectID]
		if !ok || (!authenticated && bucket != "public-bucket") {
			w.WriteHeader(statusForMissingOrPrivate(ok))
			return
		}
		writeProbeHeaders(w, object)
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		object, ok := s.objects[objectID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !authenticated && bucket != "public-bucket" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		writeProbeHeaders(w, object)
		if r.Header.Get("Range") == "bytes=0-3" {
			w.Header().Set("Content-Range", "bytes 0-3/"+strconv.Itoa(len(object.body)))
			w.Header().Set("Content-Length", "4")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(object.body[:4])
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, bytes.NewReader(object.body))
	case http.MethodDelete:
		if !authenticated {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		delete(s.objects, objectID)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func splitBucketKey(rawPath string) (bucket, key string) {
	parts := strings.SplitN(strings.Trim(rawPath, "/"), "/", 2)
	if len(parts) != 2 {
		return strings.Join(parts, ""), ""
	}
	return parts[0], parts[1]
}

func statusForMissingOrPrivate(exists bool) int {
	if exists {
		return http.StatusForbidden
	}
	return http.StatusNotFound
}

func writeProbeHeaders(w http.ResponseWriter, object storedProbeObject) {
	w.Header().Set("Content-Type", object.contentType)
	w.Header().Set("Cache-Control", object.cacheControl)
	w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
	w.Header().Set("ETag", `"probe-etag"`)
}
