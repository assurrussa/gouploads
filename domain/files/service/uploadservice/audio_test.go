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
		MaxFileSize: 50 << 20, AllowedExtensions: []string{".mp3", ".wav", ".m4a"},
		AllowedMimeTypes: map[string][]string{".mp3": {"audio/mpeg"}, ".wav": {"audio/x-wav"}, ".m4a": {"audio/mp4"}},
	}
}

func TestAudioReaderAndStoredUseOriginalFinalization(t *testing.T) {
	for _, sample := range []struct {
		name, extension, mime, suppliedMIME string
		body                                []byte
	}{
		{"tagged", ".mp3", "audio/mpeg", "audio/mp3", audiofixture.MP3},
		{"untagged", ".mp3", "audio/mpeg", "audio/mpeg", audiofixture.UntaggedMP3()},
		{"large tag", ".mp3", "audio/mpeg", "audio/mpeg", audiofixture.MP3WithTag(4096)},
		{"WAV", ".wav", "audio/wav", "audio/x-wav", audiofixture.WAV},
		{"M4A", ".m4a", "audio/mp4", "audio/x-m4a", audiofixture.M4A},
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
		{"spoofed M4A", "tone.m4a", []byte("plain text"), audioPolicy(), 10},
		{"truncated M4A", "tone.m4a", audiofixture.M4A[:12], audioPolicy(), 12},
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

func TestLargeID3ForgeryIsRejectedByReaderAndStoredIngress(t *testing.T) {
	body := audiofixture.MP3WithTag(4096)
	copy(body[4106:], "junk")
	for _, ingress := range []string{"reader", "stored"} {
		t.Run(ingress, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: body}
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				ts.mockOutboxPutter, ts.mockFileRepository, logger.Discard(), storage), config.ProcessingOriginalOnly)
			require.NoError(t, err)
			req := uploadservice.ReaderRequest{
				UploaderUUID: identity.NewUserID(), ManagerID: 12,
				ObjectType: shared.ObjectTypeAdmin, ObjectID: 12, Config: audioPolicy(),
			}
			if ingress == "stored" {
				_, err = svc.UploadStored(ctx, req, uploadservice.UploadedFile{
					OriginalName: "forged.mp3",
					FileName:     "source.mp3", Path: "tmp/uploads/source.mp3", Size: int64(len(body)), MimeType: "audio/mpeg",
				})
			} else {
				_, err = svc.UploadReader(ctx, req, uploadservice.ReaderUploadInput{
					OriginalName: "forged.mp3",
					Size:         int64(len(body)), Reader: bytes.NewReader(body),
				})
			}
			require.Error(t, err) // No writes/transaction/jobs are permitted for the forged frame.
		})
	}
}

// An existing media-resizer host can opt an .m4a name into its video policy.
// Such files must remain video through both direct and stored upload paths.
func TestLegacyM4AWithVideoPolicyRemainsVideo(t *testing.T) {
	body := []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00isommp42")
	for _, ingress := range []string{"reader", "stored"} {
		t.Run(ingress, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: body}
			queue := &associatedUploadQueue{MockoutboxPutter: ts.mockOutboxPutter}
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				queue, ts.mockFileRepository, logger.Discard(), storage), config.ProcessingMediaResizer)
			require.NoError(t, err)
			const key = "tmp/uploads/admin/12/source.m4a"
			if ingress == "reader" {
				ts.mockFileStorage.EXPECT().SaveTemp(gomock.Any(), gomock.Any()).Return(
					filestorage.StoredFile{RelativePath: key, Size: int64(len(body)), MimeType: "video/mp4"}, nil)
			}
			executeTx(ts)
			ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
			req := uploadservice.ReaderRequest{
				UploaderUUID: identity.NewUserID(), ManagerID: 12,
				ObjectType: shared.ObjectTypeAdmin, ObjectID: 12,
				Config: &uploadservice.FileUploadConfig{
					AllowedExtensions: []string{".m4a"},
					AllowedMimeTypes:  map[string][]string{".m4a": {"video/mp4"}},
				},
			}
			var result model.File
			if ingress == "stored" {
				result, err = svc.UploadStored(ctx, req, uploadservice.UploadedFile{
					OriginalName: "source.m4a", FileName: "source.m4a", Path: key,
					Size: int64(len(body)), MimeType: "video/mp4",
				})
			} else {
				result, err = svc.UploadReader(ctx, req, uploadservice.ReaderUploadInput{
					OriginalName: "source.m4a", Size: int64(len(body)), Reader: bytes.NewReader(body),
				})
			}
			require.NoError(t, err)
			require.Equal(t, model.FileTypeVideo, result.FileType)
			require.Equal(t, "video/mp4", result.MimeType)
		})
	}
}
