package http_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
)

func TestLocalTusAcceptsSmallChunksAcrossHandlers(t *testing.T) {
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	for _, chunkSize := range []int{1, 4} {
		t.Run(strconv.Itoa(chunkSize), func(t *testing.T) {
			fixture := newFixture(t)
			session, err := fixture.store.Create(context.Background(), tusupload.CreateRequest{
				UploadLength: int64(data.Len()), OriginalName: "photo.png", FileName: "source.png",
				OwnerID: 101, OwnerUUID: fixture.owner,
				Metadata: map[string]string{"filename": "photo.png", "entity_type": "exercise", "entity_id": "42"},
			})
			require.NoError(t, err)
			for offset := 0; offset < data.Len(); offset += chunkSize {
				end := min(offset+chunkSize, data.Len())
				response, _ := send(t, http.MethodPatch, "/tus/:id", "/tus/"+session.ID,
					fixture.handler.TusPatch, bytes.NewReader(data.Bytes()[offset:end]), map[string]string{
						"Tus-Resumable": tusupload.Version, "Upload-Offset": strconv.Itoa(offset),
						"Content-Type": tusupload.ContentType,
					})
				require.Equal(t, http.StatusNoContent, response.StatusCode)
			}
			stored, err := fixture.store.Get(context.Background(), session.ID)
			require.NoError(t, err)
			require.Equal(t, "image/png", stored.MimeType)
			require.EqualValues(t, data.Len(), stored.Offset)
		})
	}
}
