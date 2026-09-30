package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	"github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

type patchProbeStore struct {
	prefixOverride []byte
	tusupload.Store
	session                   tusupload.Session
	appends, deletes, creates int
	body                      []byte
}

func (s *patchProbeStore) Create(_ context.Context, req tusupload.CreateRequest) (tusupload.Session, error) {
	s.creates++
	s.session.UploadLength = req.UploadLength
	return s.session, nil
}

func (s *patchProbeStore) Get(context.Context, string) (tusupload.Session, error) {
	return s.session, nil
}

func (s *patchProbeStore) Append(_ context.Context, _ string, offset int64, body []byte, _ string) (int64, error) {
	s.appends++
	s.body = append(s.body, body...)
	s.session.Offset = offset + int64(len(body))
	return s.session.Offset, nil
}

func (s *patchProbeStore) ReadPrefix(_ context.Context, _ string, offset int64) ([]byte, error) {
	if offset != s.session.Offset {
		return nil, tusupload.ErrOffsetMismatch
	}
	if s.prefixOverride != nil {
		return s.prefixOverride, nil
	}
	return append([]byte(nil), s.body[:min(len(s.body), tusupload.SniffLen)]...), nil
}

func (s *patchProbeStore) Complete(context.Context, string) (tusupload.CompleteResult, error) {
	return tusupload.CompleteResult{
		Reader:          io.NopCloser(bytes.NewReader(s.body)),
		OriginalName:    s.session.OriginalName,
		Size:            s.session.UploadLength,
		FinalizationKey: s.session.FinalizationKey,
	}, nil
}
func (s *patchProbeStore) Delete(context.Context, string) error { s.deletes++; return nil }

func newPatchProbe(t *testing.T) (*Handler, *patchProbeStore, uploadstrategies.UploadContext) {
	t.Helper()
	actor := uploadstrategies.UploadContext{UserID: 123, UserUUID: identity.NewUserID()}
	session := tusupload.Session{
		ID:              uuid.NewString(),
		UploadLength:    128,
		OriginalName:    "photo.png",
		OwnerID:         actor.UserID,
		OwnerUUID:       actor.UserUUID,
		Metadata:        map[string]string{"filename": "photo.png", "entity_type": "exercise", "entity_id": "42"},
		FinalizationKey: uuid.NewString(),
	}
	store := &patchProbeStore{session: session}
	builder := func(_ context.Context, metadata map[string]string) (uploadstrategies.UploadContext, error) {
		resolved := actor
		resolved.Metadata = metadata
		return resolved, nil
	}
	handler := NewHandlerWithPolicy(nil, nil, store, logger.Discard(), builder, nil, HandlerPolicy{
		AllowDefaultStrategy: true,
		TrustRouteGuards:     true,
	})
	return handler, store, actor
}

func patchRequest(t *testing.T, h *Handler, store *patchProbeStore, body string) *stdhttp.Response {
	t.Helper()
	app := fiber.New()
	app.Patch("/tus/:id", h.TusPatch)
	req := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPatch,
		"/tus/"+store.session.ID, strings.NewReader(body))
	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Upload-Offset", strconv.FormatInt(store.session.Offset, 10))
	req.Header.Set("Content-Type", tusupload.ContentType)
	response, err := app.Test(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestHardeningTusMIMERejectDoesNotAppend(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	response := patchRequest(t, handler, store, "this is not a PNG")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusBadRequest, response.StatusCode)
	require.Zero(t, store.appends)
	require.Zero(t, store.session.Offset)
	require.Empty(t, store.body)
}

func TestHardeningTusBoundsCustomStorePrefix(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	store.prefixOverride = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, tusupload.SniffLen)...)
	store.session.Offset = int64(len(store.prefixOverride))
	store.session.UploadLength = store.session.Offset + 1
	response := patchRequest(t, handler, store, "x")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusNoContent, response.StatusCode)
	require.Equal(t, 1, store.appends)
	require.Equal(t, store.session.UploadLength, store.session.Offset)
}

func TestHardeningTusSmallChunksWaitForMIMEHeader(t *testing.T) {
	for _, signature := range []string{"\x89PNG\r\n\x1a\n", "%PDF-1.7\n"} {
		t.Run(stdhttp.DetectContentType([]byte(signature)), func(t *testing.T) {
			handler, store, _ := newPatchProbe(t)
			if signature[0] == '%' {
				store.session.Metadata["filename"] = "file.pdf"
			}
			for _, value := range []byte(signature) {
				response := patchRequest(t, handler, store, string([]byte{value}))
				require.NoError(t, response.Body.Close())
				require.Equal(t, stdhttp.StatusNoContent, response.StatusCode)
				require.NoError(t, response.Body.Close())
			}
			require.Equal(t, []byte(signature), store.body)
		})
	}
	handler, store, _ := newPatchProbe(t)
	response := patchRequest(t, handler, store, "\x89")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusNoContent, response.StatusCode)
	require.NoError(t, response.Body.Close())
	response = patchRequest(t, handler, store, "junk")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusBadRequest, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.Equal(t, []byte{0x89}, store.body)
	require.EqualValues(t, 1, store.session.Offset)
}

func TestHardeningTusIdentityErrorDoesNotAppend(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	handler.contextBuilder = func(context.Context, map[string]string) (uploadstrategies.UploadContext, error) {
		return uploadstrategies.UploadContext{}, errors.New("session expired")
	}
	response := patchRequest(t, handler, store, "ignored")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusUnauthorized, response.StatusCode)
	require.Zero(t, store.appends)
}

func TestHardeningTusUUIDOwnerIsAuthoritative(t *testing.T) {
	handler, store, actor := newPatchProbe(t)
	store.session.OwnerID = 0
	require.True(t, handler.isTusOwner(actor, store.session))
	stranger := actor
	stranger.UserUUID = identity.NewUserID()
	require.False(t, handler.isTusOwner(stranger, store.session))
	store.session.OwnerUUID = identity.UserID{}
	require.False(t, handler.isTusOwner(actor, store.session))
}

func TestHardeningTusHeadIsNotCacheable(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	app := fiber.New()
	app.Head("/tus/:id", handler.TusHead)
	req := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodHead, "/tus/"+store.session.ID, nil)
	req.Header.Set("Tus-Resumable", tusupload.Version)
	response, err := app.Test(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, stdhttp.StatusOK, response.StatusCode)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
}

func TestHardeningUnknownStrategyCannotFallBack(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	store.session.Metadata["context"] = "misspelled-private-context"
	response := patchRequest(t, handler, store, "ignored")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusForbidden, response.StatusCode)
	require.Zero(t, store.appends)
}

func TestHardeningStrictPolicyRequiresObjectAuthorization(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	handler.policy.TrustRouteGuards = false
	response := patchRequest(t, handler, store, "ignored")
	require.NoError(t, response.Body.Close())
	require.Equal(t, stdhttp.StatusForbidden, response.StatusCode)
	require.Zero(t, store.appends)
}

type sharedConfigStrategy struct {
	cfg *uploadservice.FileUploadConfig
}

func TestHardeningTusCreateEnforcesEffectiveLimitsBeforeStorage(t *testing.T) {
	for _, tt := range []struct {
		name          string
		limit, length int64
		filename      string
		status        int
	}{
		{"default_limit", 0, 1 << 30, "file.pdf", 413},
		{"absolute_limit", filepolicy.MaxFileSize * 2, filepolicy.MaxFileSize + 1, "file.pdf", 413},
		{"negative_limit", -1, 10, "file.pdf", 500},
		{"default_extensions", 0, 10, "file.exe", 400},
		{"defaults_accept", 0, 10, "file.pdf", 201},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, store, _ := newPatchProbe(t)
			handler.RegisterStrategy("private", sharedConfigStrategy{cfg: &uploadservice.FileUploadConfig{MaxFileSize: tt.limit}})
			app := fiber.New()
			app.Post("/tus", handler.TusCreate)
			req := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/tus", nil)
			req.Header.Set("Tus-Resumable", tusupload.Version)
			req.Header.Set("Upload-Length", strconv.FormatInt(tt.length, 10))
			values := map[string]string{"filename": tt.filename, "context": "private", "entity_type": "exercise", "entity_id": "42"}
			parts := make([]string, 0, len(values))
			for name, value := range values {
				parts = append(parts, name+" "+base64.StdEncoding.EncodeToString([]byte(value)))
			}
			req.Header.Set("Upload-Metadata", strings.Join(parts, ","))
			response, err := app.Test(req)
			require.NoError(t, err)
			require.Equal(t, tt.status, response.StatusCode)
			require.NoError(t, response.Body.Close())
			if tt.status == stdhttp.StatusCreated {
				require.Equal(t, 1, store.creates)
			} else {
				require.Zero(t, store.creates)
			}
		})
	}
}

func (s sharedConfigStrategy) CanUpload(context.Context, uploadstrategies.UploadContext) error {
	return nil
}

func (s sharedConfigStrategy) GetConfig(context.Context, uploadstrategies.UploadContext) *uploadservice.FileUploadConfig {
	return s.cfg
}

func (s sharedConfigStrategy) GetAfterJobs(
	context.Context, uploadstrategies.UploadContext) ([]fileshared.FileEventAfterJob, error,
) {
	return nil, nil
}

func TestHardeningStrategyConfigIsNotMutated(t *testing.T) {
	handler, _, actor := newPatchProbe(t)
	cfg := uploadservice.DefaultFileUploadConfig()
	handler.RegisterStrategy("private", sharedConfigStrategy{cfg: cfg})
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		resolved, err := handler.resolveUploadStrategy(reqCtx(c), actor, fileshared.ObjectTypeExercise,
			42, "private", model.FileTypeImage, true, resolveUploadStrategyOptions{
				checkCanUpload: true,
			})
		require.NoError(t, err)
		require.True(t, resolved.config.SkipResizer)
		resolved.config.AllowedExtensions[0] = ".changed"
		resolved.config.AllowedMimeTypes[".png"][0] = "changed"
		return c.SendStatus(204)
	})
	response, err := app.Test(httptest.NewRequestWithContext(context.Background(), stdhttp.MethodGet, "/", nil))
	require.NoError(t, err)
	defer response.Body.Close()
	require.False(t, cfg.SkipResizer)
	require.NotEqual(t, ".changed", cfg.AllowedExtensions[0])
	require.Equal(t, "image/png", cfg.AllowedMimeTypes[".png"][0])
}

type completedUploadProbe struct {
	TaskUploader
	mu    sync.Mutex
	keys  map[string]model.File
	calls int
}

func (p *completedUploadProbe) UploadReader(
	_ context.Context, req uploadservice.ReaderRequest, input uploadservice.ReaderUploadInput) (model.File, error,
) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if file, ok := p.keys[req.FinalizationKey]; ok {
		return file, nil
	}
	if req.FinalizationKey == "" {
		return model.File{}, errors.New("missing finalization key")
	}
	_, err := io.Copy(io.Discard, input.Reader)
	if err != nil {
		return model.File{}, err
	}
	id := req.ObjectID
	file := model.File{
		ID:               100,
		ObjectType:       req.ObjectType,
		ObjectID:         &id,
		FileName:         "queued.pdf",
		OriginalFileName: input.OriginalName,
		Size:             input.Size,
		Data:             &model.FileData{Uploader: fileshared.FileUploader{Status: fileshared.FileUploadTaskStatusQueued}},
	}
	p.keys[req.FinalizationKey] = file
	return file, nil
}

func TestHardeningHTTPCompleteRetainsRetrySessionAndKey(t *testing.T) {
	handler, store, _ := newPatchProbe(t)
	store.body = []byte("%PDF-1.7\nbody")
	store.session.OriginalName = "file.pdf"
	store.session.Metadata["filename"] = "file.pdf"
	store.session.UploadLength = int64(len(store.body))
	store.session.Offset = store.session.UploadLength
	uploader := &completedUploadProbe{keys: make(map[string]model.File)}
	handler.taskUploader = uploader
	app := fiber.New()
	app.Post("/tus/:id/complete", handler.TusComplete)
	for range 2 {
		req := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/tus/"+store.session.ID+"/complete", nil)
		req.Header.Set("Tus-Resumable", tusupload.Version)
		response, err := app.Test(req)
		require.NoError(t, err)
		require.Equal(t, 202, response.StatusCode)
		_ = response.Body.Close()
	}
	require.Zero(t, store.deletes)
	require.Equal(t, 2, uploader.calls)
	require.Len(t, uploader.keys, 1)
	// This verifies HTTP forwarding only. PostgreSQL integration separately proves
	// application-level creation is atomic and not merely deduplicated by a fake.
}
