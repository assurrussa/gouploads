package http

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	uploadmocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	"github.com/assurrussa/gouploads/internal/audiofixture"
	"github.com/assurrussa/gouploads/internal/filepolicy"
)

func newPreflightService(t *testing.T, mode config.ProcessingMode) *uploadservice.Service {
	t.Helper()
	ctrl := gomock.NewController(t)
	service, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(
		uploadmocks.NewMocktransactor(ctrl), uploadmocks.NewMockoutboxPutter(ctrl),
		uploadmocks.NewMockfileRepository(ctrl), logger.Discard(), uploadmocks.NewMockfileStorage(ctrl)), mode)
	require.NoError(t, err)
	return service
}

func preflightCreateRequest(t *testing.T, handler *Handler, filename string, length int64, cms bool) *stdhttp.Response {
	t.Helper()
	app := fiber.New()
	contextName := "policy"
	if cms {
		contextName = "cms"
		app.Post("/tus", handler.TusCreateCMS)
	} else {
		app.Post("/tus", handler.TusCreate)
	}
	req := httptest.NewRequestWithContext(context.Background(), stdhttp.MethodPost, "/tus", nil)
	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Upload-Length", strconv.FormatInt(length, 10))
	// Client MIME and file_type are intentionally omitted: neither is required.
	metadata := map[string]string{"filename": filename, "context": contextName, "entity_type": "exercise", "entity_id": "42"}
	parts := make([]string, 0, len(metadata))
	for key, value := range metadata {
		parts = append(parts, key+" "+base64.StdEncoding.EncodeToString([]byte(value)))
	}
	req.Header.Set("Upload-Metadata", strings.Join(parts, ","))
	response, err := app.Test(req)
	require.NoError(t, err)
	return response
}

func TestTusCreatePreflightsOriginalPolicyBeforeStorage(t *testing.T) {
	for _, tt := range []struct {
		name     string
		mode     config.ProcessingMode
		cms      bool
		filename string
		cfg      *uploadservice.FileUploadConfig
		status   int
	}{
		{
			name: "unsupported_original", mode: config.ProcessingOriginalOnly, filename: "note.txt",
			cfg: preflightPolicy(".txt", "text/plain"), status: stdhttp.StatusInternalServerError,
		},
		{
			name: "unsupported_default_mode", filename: "note.txt",
			cfg: preflightPolicy(".txt", "text/plain"), status: stdhttp.StatusInternalServerError,
		},
		{
			name: "missing_effective_mime", mode: config.ProcessingOriginalOnly, filename: "file.pdf",
			cfg: &uploadservice.FileUploadConfig{
				AllowedExtensions: []string{".pdf"}, AllowedMimeTypes: map[string][]string{".png": {"image/png"}},
			}, status: stdhttp.StatusInternalServerError,
		},
		{
			name: "blank_only_extensions", mode: config.ProcessingOriginalOnly, filename: "note.txt",
			cfg:    &uploadservice.FileUploadConfig{AllowedExtensions: []string{" ", ""}},
			status: stdhttp.StatusInternalServerError,
		},
		{
			name: "oversized_policy", mode: config.ProcessingOriginalOnly, filename: "file.pdf",
			cfg: &uploadservice.FileUploadConfig{MaxFileSize: filepolicy.MaxFileSize + 1}, status: stdhttp.StatusInternalServerError,
		},
		{
			name: "default_policy_without_client_mime", mode: config.ProcessingOriginalOnly, filename: "file.pdf",
			cfg: &uploadservice.FileUploadConfig{}, status: stdhttp.StatusCreated,
		},
		{
			name: "normalized_audio_opt_in", mode: config.ProcessingOriginalOnly, filename: "voice.wav",
			cfg: &uploadservice.FileUploadConfig{
				AllowedExtensions: []string{" WAV "}, AllowedMimeTypes: map[string][]string{".wav": {"audio/x-wav"}},
			}, status: stdhttp.StatusCreated,
		},
		{
			name: "media_policy_unaffected", mode: config.ProcessingMediaResizer, filename: "note.txt",
			cfg: preflightPolicy(".txt", "text/plain"), status: stdhttp.StatusCreated,
		},
		{
			name: "cms_policy_unaffected", mode: config.ProcessingOriginalOnly, cms: true, filename: "note.txt",
			cfg: preflightPolicy(".txt", "text/plain"), status: stdhttp.StatusCreated,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler, store, _ := newPatchProbe(t)
			handler.taskUploader = newPreflightService(t, tt.mode)
			contextName := "policy"
			if tt.cms {
				contextName = "cms"
			}
			handler.RegisterStrategy(contextName, sharedConfigStrategy{cfg: tt.cfg})
			response := preflightCreateRequest(t, handler, tt.filename, 10, tt.cms)
			require.NoError(t, response.Body.Close())
			require.Equal(t, tt.status, response.StatusCode)
			if tt.status == stdhttp.StatusCreated {
				require.Equal(t, 1, store.creates)
			} else {
				require.Zero(t, store.creates)
			}
		})
	}
}

// The callback replaces only database finalization locking. HTTP admission,
// filesystem TUS storage and completion content validation remain real.
type preflightFinalizationRepository struct {
	*uploadmocks.MockfileRepository
}

func (r *preflightFinalizationRepository) FinalizeUpload(
	ctx context.Context, _, _ string, _ bool, create func(context.Context) (model.File, error),
) (model.File, error) {
	return create(ctx)
}

func TestTusPreflightDoesNotApproveFinalContentOrPolicy(t *testing.T) {
	for _, name := range []string{"forged_large_id3", "changed_stored_bytes", "changed_policy"} {
		t.Run(name, func(t *testing.T) {
			body := []byte("%PDF-1.7\nprivate PDF contents\n")
			filename := "file.pdf"
			policy := &uploadservice.FileUploadConfig{}
			if name == "forged_large_id3" {
				body = audiofixture.MP3WithTag(4096)
				copy(body[4106:], "junk")
				filename = "voice.mp3"
				policy = preflightPolicy(".mp3", "audio/mpeg")
			}
			handler, _, _ := newPatchProbe(t)
			ctrl := gomock.NewController(t)
			service, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(
				uploadmocks.NewMocktransactor(ctrl), uploadmocks.NewMockoutboxPutter(ctrl),
				&preflightFinalizationRepository{uploadmocks.NewMockfileRepository(ctrl)},
				logger.Discard(), uploadmocks.NewMockfileStorage(ctrl)), config.ProcessingOriginalOnly)
			require.NoError(t, err)
			handler.taskUploader = service
			root := t.TempDir()
			store, err := tusupload.NewFileStore(root)
			require.NoError(t, err)
			handler.tusStore = store
			handler.RegisterStrategy("policy", sharedConfigStrategy{cfg: policy})
			created := preflightCreateRequest(t, handler, filename, int64(len(body)), false)
			require.NoError(t, created.Body.Close())
			require.Equal(t, stdhttp.StatusCreated, created.StatusCode)
			location := created.Header.Get("Location")

			app := fiber.New()
			app.Patch("/tus/:id", handler.TusPatch)
			app.Post("/tus/:id/complete", handler.TusComplete)
			request := httptest.NewRequestWithContext(t.Context(), stdhttp.MethodPatch, location, bytes.NewReader(body))
			request.Header.Set("Tus-Resumable", tusupload.Version)
			request.Header.Set("Upload-Offset", "0")
			request.Header.Set("Content-Type", tusupload.ContentType)
			patched, err := app.Test(request)
			require.NoError(t, err)
			require.NoError(t, patched.Body.Close())
			require.Equal(t, stdhttp.StatusNoContent, patched.StatusCode)
			if name == "changed_stored_bytes" {
				// A previously sniffed MIME cannot approve bytes read again at completion.
				path := filepath.Join(root, filepath.Base(location), "data")
				require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), len(body)), 0o600))
			}
			if name == "changed_policy" {
				handler.RegisterStrategy("policy", sharedConfigStrategy{cfg: &uploadservice.FileUploadConfig{
					AllowedExtensions: []string{".pdf"}, AllowedMimeTypes: map[string][]string{".pdf": {"text/plain"}},
				}})
			}
			request = httptest.NewRequestWithContext(t.Context(), stdhttp.MethodPost, location+"/complete", nil)
			request.Header.Set("Tus-Resumable", tusupload.Version)
			completed, err := app.Test(request)
			require.NoError(t, err)
			result, err := io.ReadAll(completed.Body)
			require.NoError(t, err)
			require.NoError(t, completed.Body.Close())
			expected := stdhttp.StatusBadRequest
			if name == "changed_policy" {
				expected = stdhttp.StatusInternalServerError
			}
			require.Equal(t, expected, completed.StatusCode, string(result))
			// No metadata creation, final storage or outbox calls are allowed.
		})
	}
}

func preflightPolicy(extension, mime string) *uploadservice.FileUploadConfig {
	return &uploadservice.FileUploadConfig{
		AllowedExtensions: []string{extension}, AllowedMimeTypes: map[string][]string{extension: {mime}},
	}
}

// Prefix fixtures exercise admission and tiny first chunks, not full decoding.
// Complete original payloads and publication are covered by originals integration.
func TestTusAdmissionFormatsAndSmallChunksWithoutClientMIME(t *testing.T) {
	for _, sample := range []struct{ extension, mime, signature string }{
		{".jpg", "image/jpeg", "\xff\xd8\xff"},
		{".jpeg", "image/jpeg", "\xff\xd8\xff"},
		{".png", "image/png", "\x89PNG\r\n\x1a\n"},
		{".gif", "image/gif", "GIF89a"},
		{".webp", "image/webp", "RIFF\x18\x00\x00\x00WEBPVP"},
		{".mp4", "video/mp4", "\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00isommp42"},
		{".webm", "video/webm", "\x1a\x45\xdf\xa3"},
		{".pdf", "application/pdf", "%PDF-"},
		{".mp3", "audio/mpeg", "ID3\x03\x00\x00\x00\x00\x00\x00"},
		{".wav", "audio/wav", "RIFF\x18\x00\x00\x00WAVE"},
	} {
		policies := map[string]*uploadservice.FileUploadConfig{"explicit": preflightPolicy(sample.extension, sample.mime)}
		if !strings.HasPrefix(sample.mime, "audio/") {
			policies["default"] = &uploadservice.FileUploadConfig{}
		}
		if strings.HasPrefix(sample.mime, "image/") {
			policies["rich_text"] = uploadservice.DefaultFileUploadRichTextConfig()
		}
		for name, policy := range policies {
			t.Run(sample.extension+"/"+name, func(t *testing.T) {
				handler, _, _ := newPatchProbe(t)
				handler.taskUploader = newPreflightService(t, config.ProcessingOriginalOnly)
				store, err := tusupload.NewFileStore(t.TempDir())
				require.NoError(t, err)
				handler.tusStore = store
				handler.RegisterStrategy("policy", sharedConfigStrategy{cfg: policy})
				created := preflightCreateRequest(t, handler, "upload"+sample.extension, int64(len(sample.signature)), false)
				require.NoError(t, created.Body.Close())
				require.Equal(t, stdhttp.StatusCreated, created.StatusCode)
				location := created.Header.Get("Location")
				app := fiber.New()
				app.Patch("/tus/:id", handler.TusPatch)
				for offset := range len(sample.signature) {
					req := httptest.NewRequestWithContext(t.Context(), stdhttp.MethodPatch, location,
						strings.NewReader(sample.signature[offset:offset+1]))
					req.Header.Set("Tus-Resumable", tusupload.Version)
					req.Header.Set("Upload-Offset", strconv.Itoa(offset))
					req.Header.Set("Content-Type", tusupload.ContentType)
					response, requestErr := app.Test(req)
					require.NoError(t, requestErr)
					require.NoError(t, response.Body.Close())
					require.Equal(t, stdhttp.StatusNoContent, response.StatusCode, "offset %d", offset)
				}
				session, err := store.Get(t.Context(), filepath.Base(location))
				require.NoError(t, err)
				require.EqualValues(t, len(sample.signature), session.Offset)
				require.Equal(t, sample.mime, session.MimeType)
			})
		}
	}
}
