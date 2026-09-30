package host_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

type ctxTestKey struct{}

func TestStandardUploadHandler_InvalidUploadHandler(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		handler *host.UploadHandler
	}{
		{name: "nil"},
		{name: "zero value", handler: &host.UploadHandler{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stdHandler, err := host.NewStandardUploadHandler(tc.handler, "/files")
			require.Nil(t, stdHandler)
			require.ErrorIs(t, err, host.ErrNilUploadHandler)
		})
	}
}

func TestStandardUploadHandler_MultipleConfigs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfgs []host.StandardUploadHandlerConfig
	}{
		{name: "two", cfgs: []host.StandardUploadHandlerConfig{{BodyLimit: 1024}, {BodyLimit: 2048}}},
		{name: "three", cfgs: []host.StandardUploadHandlerConfig{{}, {}, {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
			stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files", tc.cfgs...)
			require.Nil(t, stdHandler)
			require.ErrorIs(t, err, host.ErrInvalidStandardHandlerConfig)
		})
	}
}

func TestStandardUploadHandler_DefaultConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cfgs []host.StandardUploadHandlerConfig
	}{
		{name: "omitted"},
		{name: "zero", cfgs: []host.StandardUploadHandlerConfig{{BodyLimit: 0}}},
		{name: "negative", cfgs: []host.StandardUploadHandlerConfig{{BodyLimit: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := mockChunkSizeStore{chunkSize: host.DefaultBodyLimit}
			uploadHandler := host.NewUploadHandler(nil, nil, store, nil, nil, nil)
			stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files", tc.cfgs...)
			require.NoError(t, err)
			require.NotNil(t, stdHandler)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/files/tus", nil)
			rec := httptest.NewRecorder()
			stdHandler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusNoContent, rec.Code)
			require.NotEmpty(t, rec.Header().Get("Tus-Version"))

			store.chunkSize++
			oversizedHandler := host.NewUploadHandler(nil, nil, store, nil, nil, nil)
			stdHandler, err = host.NewStandardUploadHandler(oversizedHandler, "/files", tc.cfgs...)
			require.Nil(t, stdHandler)
			require.ErrorIs(t, err, host.ErrIncompatibleBodyLimit)
		})
	}
}

func TestStandardUploadHandler_TusOptions(t *testing.T) {
	t.Parallel()

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/files/tus", nil)
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Tus-Version"))
	require.NotEmpty(t, rec.Header().Get("Tus-Resumable"))
}

func TestStandardUploadHandler_NotFound(t *testing.T) {
	t.Parallel()

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/other/unknown-path", nil)
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestStandardUploadHandler_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/files/tus", nil)
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestStandardUploadHandler_ContextValuePropagation(t *testing.T) {
	t.Parallel()

	var receivedVal any
	cb := func(ctx context.Context, _ map[string]string) (host.UploadContext, error) {
		receivedVal = ctx.Value(ctxTestKey{})
		return host.UploadContext{UserUUID: host.NewUserID()}, nil
	}

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, cb, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), ctxTestKey{}, "custom-auth-token-or-claims")
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/files/tus", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", "100")
	req.Header.Set("Upload-Metadata", "filename dGVzdC5qcGc=") // base64 "test.jpg"
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Equal(t, "custom-auth-token-or-claims", receivedVal)
}

func TestStandardUploadHandler_ContextCancellation(t *testing.T) {
	t.Parallel()

	var receivedErr error
	cb := func(ctx context.Context, _ map[string]string) (host.UploadContext, error) {
		receivedErr = ctx.Err()
		return host.UploadContext{UserUUID: host.NewUserID()}, nil
	}

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, cb, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before request

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/files/tus", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", "100")
	req.Header.Set("Upload-Metadata", "filename dGVzdC5qcGc=")
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Equal(t, context.Canceled, receivedErr)
}

func TestStandardUploadHandler_5MiBPatch_Not413(t *testing.T) {
	t.Parallel()

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	// 5 MiB payload representing a standard TUS chunk
	chunk5MiB := bytes.Repeat([]byte("x"), 5*1024*1024)
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/files/tus/nonexistent-session",
		bytes.NewReader(chunk5MiB),
	)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", "0")
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	// In Fiber v3.5.0, default BodyLimit is 4 MiB, which would fail with 413 Payload Too Large.
	// With gouploads StandardUploadHandler, default limit is 32 MiB (DefaultBodyLimit), so 5 MiB chunk is allowed
	// through to the handler logic (which returns 401/404/etc, NOT 413).
	require.NotEqual(t, http.StatusRequestEntityTooLarge, rec.Code)

	// Now verify that custom BodyLimit is respected:
	smallHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files", host.StandardUploadHandlerConfig{
		BodyLimit: 1024, // 1 KiB
	})
	require.NoError(t, err)
	reqSmall := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/files/tus/nonexistent-session",
		bytes.NewReader(bytes.Repeat([]byte("x"), 2048)),
	)
	reqSmall.Header.Set("Tus-Resumable", "1.0.0")
	reqSmall.Header.Set("Content-Type", "application/offset+octet-stream")
	reqSmall.Header.Set("Upload-Offset", "0")
	recSmall := httptest.NewRecorder()

	smallHandler.ServeHTTP(recSmall, reqSmall)
	require.Equal(t, http.StatusRequestEntityTooLarge, recSmall.Code)
}

func TestStandardUploadHandler_AuthorizerContextPropagation(t *testing.T) {
	t.Parallel()

	var receivedCtxVal any
	policy := host.UploadHandlerPolicy{
		AllowDefaultStrategy: true,
		Authorize: func(ctx context.Context, _ host.UploadContext, _ string, _ host.File) error {
			receivedCtxVal = ctx.Value(ctxTestKey{})
			return errors.New("stop after authorize")
		},
	}
	cb := func(_ context.Context, _ map[string]string) (host.UploadContext, error) {
		return host.UploadContext{UserID: 1, UserUUID: host.NewUserID()}, nil
	}
	uploadHandler := host.NewUploadHandlerWithPolicy(nil, nil, nil, nil, cb, nil, policy)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), ctxTestKey{}, "auth-token-999")
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/files/tus", nil)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", "100")
	req.Header.Set("Upload-Metadata", "filename dGVzdC5qcGc=,entity_type YWRtaW4=,entity_id NDI=")
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Equal(t, "auth-token-999", receivedCtxVal)
}

func TestStandardUploadHandler_RestoresInternalHeader(t *testing.T) {
	t.Parallel()

	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/files/tus", nil)
	rec := httptest.NewRecorder()

	stdHandler.ServeHTTP(rec, req)

	require.Empty(t, req.Header.Get("X-Internal-Gouploads-Ctx-ID"))
}

type mockChunkSizeStore struct {
	host.TusStore
	chunkSize int64
}

func (m mockChunkSizeStore) ChunkSize() int64 {
	return m.chunkSize
}

func TestStandardUploadHandler_ChunkSizeValidation(t *testing.T) {
	t.Parallel()

	// 1. 5 MiB chunk + 32 MiB default → OK
	store5MiB := mockChunkSizeStore{chunkSize: 5 * 1024 * 1024}
	handler5MiB := host.NewUploadHandler(nil, nil, store5MiB, nil, nil, nil)
	stdHandler1, err := host.NewStandardUploadHandler(handler5MiB, "/files")
	require.NoError(t, err)
	require.NotNil(t, stdHandler1)

	// 2. 32 MiB chunk + 32 MiB default → OK
	store32MiB := mockChunkSizeStore{chunkSize: 32 * 1024 * 1024}
	handler32MiB := host.NewUploadHandler(nil, nil, store32MiB, nil, nil, nil)
	stdHandler2, err := host.NewStandardUploadHandler(handler32MiB, "/files")
	require.NoError(t, err)
	require.NotNil(t, stdHandler2)

	// 3. 64 MiB chunk + default 32 MiB → constructor error (ErrIncompatibleBodyLimit)
	store64MiB := mockChunkSizeStore{chunkSize: 64 * 1024 * 1024}
	handler64MiB := host.NewUploadHandler(nil, nil, store64MiB, nil, nil, nil)
	stdHandler3, err := host.NewStandardUploadHandler(handler64MiB, "/files")
	require.Error(t, err)
	require.ErrorIs(t, err, host.ErrIncompatibleBodyLimit)
	require.Nil(t, stdHandler3)

	// 4. 64 MiB chunk + explicit 30 MiB (< 64 MiB) → constructor error (ErrIncompatibleBodyLimit)
	stdHandler4, err := host.NewStandardUploadHandler(handler64MiB, "/files", host.StandardUploadHandlerConfig{
		BodyLimit: 30 * 1024 * 1024,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, host.ErrIncompatibleBodyLimit)
	require.Nil(t, stdHandler4)

	// 5. 64 MiB chunk + explicit 70 MiB → OK
	stdHandler5, err := host.NewStandardUploadHandler(handler64MiB, "/files", host.StandardUploadHandlerConfig{
		BodyLimit: 70 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.NotNil(t, stdHandler5)

	// With explicit BodyLimit (70 MiB), a 33 MiB payload is accepted without 413
	payload33MiB := bytes.Repeat([]byte("a"), 33*1024*1024)
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/files/tus/nonexistent",
		bytes.NewReader(payload33MiB),
	)
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", "0")
	rec := httptest.NewRecorder()
	stdHandler5.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusRequestEntityTooLarge, rec.Code)
}
