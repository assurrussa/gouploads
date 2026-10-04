package uploadservice_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	finalizeoriginal "github.com/assurrussa/gouploads/domain/files/outbox/finalize_original"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/audiofixture"
	"github.com/assurrussa/gouploads/internal/identity"
)

func audioPolicy() *uploadservice.FileUploadConfig {
	return &uploadservice.FileUploadConfig{
		MaxFileSize: 50 << 20, AllowedExtensions: []string{".mp3", ".wav"},
		AllowedMimeTypes: map[string][]string{".mp3": {"audio/mpeg"}, ".wav": {"audio/x-wav"}},
	}
}

func TestAudioReaderAndStoredUseOriginalFinalization(t *testing.T) {
	for _, sample := range []struct {
		name, extension, mime, suppliedMIME string
		body                                []byte
	}{
		{"tagged", ".mp3", "audio/mpeg", "audio/mp3", audiofixture.MP3},
		{"untagged", ".mp3", "audio/mpeg", "audio/mpeg", audiofixture.UntaggedMP3()},
		{"WAV", ".wav", "audio/wav", "audio/x-wav", audiofixture.WAV},
	} {
		for _, ingress := range []string{"reader", "stored"} {
			t.Run(sample.name+"/"+ingress, func(t *testing.T) {
				ctx, cancel, ts := NewTestRepoSuite(t)
				defer cancel()
				storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: sample.body}
				svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
					ts.mockOutboxPutter, ts.mockFileRepository, logger.Discard(), storage), config.ProcessingOriginalOnly)
				require.NoError(t, err)
				name := "source" + sample.extension
				key := "tmp/uploads/admin/12/" + name
				if ingress == "reader" {
					ts.mockFileStorage.EXPECT().SaveTemp(gomock.Any(), gomock.Any()).DoAndReturn(
						func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
							body, readErr := io.ReadAll(input.Reader)
							require.NoError(t, readErr)
							require.Equal(t, sample.body, body)
							require.Equal(t, sample.mime, input.MimeType)
							return filestorage.StoredFile{RelativePath: key, Size: int64(len(body)), MimeType: input.MimeType}, nil
						})
				}
				executeTx(ts)
				ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, file model.File) (int64, error) {
						require.Equal(t, model.FileTypeAudio, file.FileType)
						require.Equal(t, sample.mime, file.MimeType)
						return 21, nil
					})
				payload, err := finalizeoriginal.MarshalPayload(finalizeoriginal.Payload{FileID: 21})
				require.NoError(t, err)
				ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), finalizeoriginal.JobName, payload, gomock.Any())
				req := uploadservice.ReaderRequest{
					UploaderUUID: identity.NewUserID(), ManagerID: 12,
					ObjectType: shared.ObjectTypeAdmin, ObjectID: 12, Config: audioPolicy(),
				}
				var result model.File
				if ingress == "stored" {
					result, err = svc.UploadStored(ctx, req, uploadservice.UploadedFile{
						OriginalName: name,
						FileName:     name, Path: key, Size: int64(len(sample.body)), MimeType: sample.suppliedMIME,
					})
				} else {
					result, err = svc.UploadReader(ctx, req, uploadservice.ReaderUploadInput{
						OriginalName: name,
						Size:         int64(len(sample.body)), Reader: bytes.NewReader(sample.body),
					})
				}
				require.NoError(t, err)
				require.Equal(t, model.FileTypeAudio, result.FileType)
			})
		}
	}
}

func TestAudioRejectsDefaultPolicySpoofingAndOversize(t *testing.T) {
	for _, tc := range []struct {
		name, filename string
		body           []byte
		policy         *uploadservice.FileUploadConfig
		size           int64
	}{
		{"default", "tone.mp3", audiofixture.MP3, nil, int64(len(audiofixture.MP3))},
		{"spoof", "tone.mp3", []byte("plain text"), audioPolicy(), 10},
		{"wrong extension", "tone.wav", audiofixture.MP3, audioPolicy(), int64(len(audiofixture.MP3))},
		{"empty", "tone.mp3", nil, audioPolicy(), 0},
		{"truncated", "tone.mp3", audiofixture.MP3[:10], audioPolicy(), 10},
		{"oversized", "tone.mp3", audiofixture.MP3, audioPolicy(), (50 << 20) + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				ts.mockOutboxPutter, ts.mockFileRepository, logger.Discard(), ts.mockFileStorage), config.ProcessingOriginalOnly)
			require.NoError(t, err)
			_, err = svc.UploadReader(ctx, uploadservice.ReaderRequest{
				UploaderUUID: identity.NewUserID(), ManagerID: 12,
				ObjectType: shared.ObjectTypeAdmin, ObjectID: 12, Config: tc.policy,
			}, uploadservice.ReaderUploadInput{
				OriginalName: tc.filename, Size: tc.size, Reader: bytes.NewReader(tc.body),
			})
			require.Error(t, err) // No storage, transaction, record or outbox expectation is registered.
		})
	}
}

func TestAudioMediaResizerRejectsBeforeAnyJobOrWrite(t *testing.T) {
	for _, ingress := range []string{"reader", "stored"} {
		t.Run(ingress, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: audiofixture.MP3}
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				ts.mockOutboxPutter, ts.mockFileRepository, logger.Discard(), storage), config.ProcessingMediaResizer)
			require.NoError(t, err)
			policy := audioPolicy()
			policy.SkipResizer = true // Still cannot route audio through an image/video pipeline.
			req := uploadservice.ReaderRequest{
				UploaderUUID: identity.NewUserID(), ManagerID: 12,
				ObjectType: shared.ObjectTypeAdmin, ObjectID: 12, Config: policy,
			}
			if ingress == "stored" {
				_, err = svc.UploadStored(ctx, req, uploadservice.UploadedFile{
					OriginalName: "tone.mp3",
					FileName:     "source.mp3", Path: "tmp/uploads/source.mp3", Size: int64(len(audiofixture.MP3)), MimeType: "audio/mpeg",
				})
			} else {
				_, err = svc.UploadReader(ctx, req, uploadservice.ReaderUploadInput{
					OriginalName: "tone.mp3",
					Size:         int64(len(audiofixture.MP3)), Reader: bytes.NewReader(audiofixture.MP3),
				})
			}
			require.ErrorContains(t, err, "audio uploads require original_only")
		})
	}
}
