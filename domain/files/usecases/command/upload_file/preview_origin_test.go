package uploadfile_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
)

// A video job owns both the video and its image previews. The image's content
// type must not make the importer switch to the independently configured image
// resizer's origin.
func TestHandleVideoPreviewUsesVideoJobOrigin(t *testing.T) {
	_, state, storage, _, request := newFinalization(t)
	state.file.FileType = model.FileTypeVideo
	state.file.MimeType = "video/mp4"
	state.file.FileName = "source.mp4"

	var imageRequests atomic.Int64
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		imageRequests.Add(1)
		http.Error(w, "video artifacts do not live here", http.StatusNotFound)
	}))
	t.Cleanup(imageServer.Close)

	mainBody, previewBody := sampleMP4Header(), pngBody(t)
	videoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Token") != "" {
			t.Error("artifact download forwarded a resizer credential")
		}
		switch r.URL.Path {
		case "/files/main":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(mainBody)
		case "/files/main_preview":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(previewBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(videoServer.Close)

	client := clientresizer.Must(clientresizer.NewOptions(
		http.DefaultClient, imageServer.URL+"/jobs", videoServer.URL+"/jobs", logger.Discard(),
		clientresizer.WithImageResizerToken("image-token-must-not-leak"),
		clientresizer.WithVideoResizerToken("video-token-must-not-leak"),
	))
	useCase := uploadfile.Must(uploadfile.NewOptions(state, state, client, state, logger.Discard(), storage, state))
	expires := time.Now().Add(time.Hour)
	request.Artifacts = []uploadfile.Artifact{
		{
			Preset: "main", URL: videoServer.URL + "/files/main", MediaType: "video",
			ContentType: "video/mp4", Size: int64(len(mainBody)), ExpireAt: expires,
		},
		{
			Preset: "main_preview", URL: videoServer.URL + "/files/main_preview", MediaType: "image",
			ContentType: "image/png", Size: int64(len(previewBody)), ExpireAt: expires,
			Metadata: map[string]any{"preview": true},
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := useCase.Handle(ctx, request)
	require.NoError(t, err)
	require.Zero(t, imageRequests.Load())
	require.Equal(t, model.FileTypeVideo, state.file.FileType)
	preview := state.file.GetData().Presets["main_preview"]
	require.True(t, preview.IsPreview)
	require.Equal(t, "image", preview.MediaType)
	require.Equal(t, "image/png", preview.MimeType)
	require.Equal(t, previewBody, storage.objects[preview.RelativePath])
	require.Equal(t, preview.RelativePath, state.file.PreferredPreviewPath())

	// Do not fix split origins by accepting either configured origin. Only the
	// server that owns this video job may supply its artifacts.
	_, err = client.DownloadFile(ctx, clientresizer.RequestDownload{
		Preset: "main_preview", URL: imageServer.URL + "/files/main_preview", TypeMedia: "video",
	})
	require.ErrorContains(t, err, "does not match")
	require.Zero(t, imageRequests.Load())
}

func TestHandleVideoJobRetainsOriginAfterMainTypeChanges(t *testing.T) {
	useCase, state, _, download, request := newFinalization(t)
	state.file.FileType = model.FileTypeVideo
	state.file.MimeType = "video/mp4"
	download.bodies["main_preview"] = pngBody(t)
	// A video may have an image output (for example, GIF). Its later preview
	// must still be fetched from the original video processor.
	request.Artifacts = append(request.Artifacts, uploadfile.Artifact{
		Preset: "main_preview", URL: "https://video.example.test/files/main_preview",
		MediaType: "image", ContentType: "image/png", ExpireAt: time.Now().Add(time.Hour),
		Metadata: map[string]any{"preview": true},
	})
	_, err := useCase.Handle(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, model.FileTypeImage, state.file.FileType)
	require.Len(t, download.requests, 2)
	for _, req := range download.requests {
		require.Equal(t, "video", req.TypeMedia)
	}
}
