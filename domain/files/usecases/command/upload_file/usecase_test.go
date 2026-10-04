package uploadfile_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"path"
	"sync"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/audiofixture"
	"github.com/assurrussa/gouploads/internal/events"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	"github.com/assurrussa/gouploads/internal/identity"
)

var errInjected = errors.New("injected storage or transaction failure")

type (
	txMarker    struct{}
	recordedJob struct{ name, payload string }
)

// The state fake rolls back metadata and outbox together, but not object
// storage. That distinction is the invariant being tested here.
type finalizationState struct {
	mu                                   sync.Mutex
	file                                 model.File
	jobs                                 []recordedJob
	events                               []any
	failUpdate, failPut, uncertainCommit bool
}

func cloneFile(file model.File) model.File {
	body, err := json.Marshal(file)
	if err != nil {
		panic(err)
	}
	var result model.File
	if err := json.Unmarshal(body, &result); err != nil {
		panic(err)
	}
	return result
}

func (s *finalizationState) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := cloneFile(s.file)
	jobs := append([]recordedJob(nil), s.jobs...)
	if err := fn(context.WithValue(ctx, txMarker{}, true)); err != nil {
		s.file = saved
		s.jobs = jobs
		return err
	}
	if s.uncertainCommit {
		s.uncertainCommit = false
		return errInjected
	}
	return nil
}

func (s *finalizationState) GetByID(context.Context, int64) (model.File, error) {
	return cloneFile(s.file), nil
}

func (s *finalizationState) GetByIDForUpdate(ctx context.Context, _ int64) (model.File, error) {
	if active, ok := ctx.Value(txMarker{}).(bool); !ok || !active {
		return model.File{}, errors.New("read was not locked in transaction")
	}
	return cloneFile(s.file), nil
}

func (s *finalizationState) Update(ctx context.Context, _ int64, file model.File) error {
	if active, ok := ctx.Value(txMarker{}).(bool); !ok || !active {
		return errors.New("update outside transaction")
	}
	if s.failUpdate {
		return errInjected
	}
	s.file = cloneFile(file)
	return nil
}

func (s *finalizationState) Put(ctx context.Context, name, payload string, _ time.Time) (outboxtypes.JobID, error) {
	if active, ok := ctx.Value(txMarker{}).(bool); !ok || !active {
		return outboxtypes.JobIDNil, errors.New("outbox outside transaction")
	}
	if s.failPut {
		return outboxtypes.JobIDNil, errInjected
	}
	s.jobs = append(s.jobs, recordedJob{name, payload})
	return outboxtypes.NewJobID(), nil
}

func (s *finalizationState) Publish(_ context.Context, _ identity.UserID, event events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

type finalizationStorage struct {
	mu       sync.Mutex
	objects  map[string][]byte
	deletes  int
	failSave bool
}

func (s *finalizationStorage) SavePersist(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failSave {
		return filestorage.StoredFile{}, errInjected
	}
	body, err := io.ReadAll(input.Reader)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	key := path.Join(input.Dir, input.FileName)
	s.objects[key] = append([]byte(nil), body...)
	return filestorage.StoredFile{RelativePath: key, Size: int64(len(body)), MimeType: input.MimeType}, nil
}

func (s *finalizationStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.objects, key)
	return nil
}

type finalizationDownload struct {
	bodies     map[string][]byte
	requests   []clientresizer.RequestDownload
	failPreset string
	nilBody    bool
	downloads  int
}

func (s *finalizationDownload) DownloadFile(ctx context.Context,
	req clientresizer.RequestDownload,
) (clientresizer.ResponseDownload, error) {
	if err := ctx.Err(); err != nil {
		return clientresizer.ResponseDownload{}, err
	}
	s.downloads++
	s.requests = append(s.requests, req)
	if req.Preset == s.failPreset {
		return clientresizer.ResponseDownload{}, errInjected
	}
	if s.nilBody {
		return clientresizer.ResponseDownload{}, nil
	}
	body := s.bodies[req.Preset]
	return clientresizer.ResponseDownload{Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
}

func pngBody(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	require.NoError(t, png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return body.Bytes()
}

func newFinalization(t *testing.T) (*uploadfile.UseCase,
	*finalizationState,
	*finalizationStorage,
	*finalizationDownload,
	uploadfile.Request,
) {
	t.Helper()
	owner := identity.NewUserID()
	objectID := shared.FileObjectID(42)
	body := pngBody(t)
	state := &finalizationState{file: model.File{
		ID:               12,
		ObjectType:       shared.ObjectTypeExercise,
		ObjectID:         &objectID,
		Slug:             uuid.NewString(),
		FolderPath:       "tmp/uploads/source",
		FileName:         "source.png",
		OriginalFileName: "photo.png",
		MimeType:         "image/png",
		FileType:         model.FileTypeImage,
		Size:             int64(len(body)),
		IsPrimary:        true,
		Data: &model.FileData{Uploader: shared.FileUploader{
			UserUUID: owner,
			UserID:   10,
			Type:     shared.UserTypeUser,
			Status:   shared.FileUploadTaskStatusQueued,
			AfterJobs: []shared.FileEventAfterJob{shared.NewFileEventAfterJob("test_job",
				owner,
				"")},
		}},
	}}
	storage := &finalizationStorage{objects: make(map[string][]byte)}
	download := &finalizationDownload{bodies: map[string][]byte{"main": body}}
	options := uploadfile.NewOptions(state,
		state,
		download,
		state,
		logger.Discard(),
		storage,
		state,
		uploadfile.WithDeliveryBaseURL("https://media.example.test"))
	useCase, err := uploadfile.New(options)
	require.NoError(t, err)
	request := uploadfile.Request{
		FileID: 12,
		Artifacts: []uploadfile.Artifact{{
			Preset:      "main",
			URL:         "https://resizer.example.test/main",
			MediaType:   "image",
			ContentType: "image/png",
			Size:        int64(len(body)),
			ExpireAt:    time.Now().Add(time.Hour),
			Metadata: map[string]any{
				"target_width":  2,
				"target_height": 2,
			},
		}},
	}
	return useCase, state, storage, download, request
}

func TestHandle_MustInit(t *testing.T) {
	require.Panics(t, func() { uploadfile.Must(uploadfile.NewOptions(nil, nil, nil, nil, nil, nil, nil)) })
}

func TestHandleRejectsNormalizedPresetCollisionsBeforeStorage(t *testing.T) {
	for _, names := range [][2]string{{"main", "MAIN"}, {"foo-bar", "foo--bar"}, {"foo bar", "foo-bar"}} {
		t.Run(names[0]+"_"+names[1], func(t *testing.T) {
			useCase, state, storage, download, request := newFinalization(t)
			for _, name := range names {
				artifact := request.Artifacts[0]
				artifact.Preset = name
				request.Artifacts = append(request.Artifacts, artifact)
				download.bodies[name] = pngBody(t)
			}
			if names[0] == "main" {
				request.Artifacts = request.Artifacts[1:]
			}
			_, err := useCase.Handle(context.Background(), request)
			require.ErrorContains(t, err, "duplicate artifact preset")
			require.Zero(t, download.downloads)
			require.Empty(t, storage.objects)
			require.Empty(t, state.jobs)
			require.Equal(t, shared.FileUploadTaskStatusQueued, state.file.GetData().Uploader.Status)
		})
	}
}

func TestHandle_Success(t *testing.T) {
	useCase, state, storage, download, request := newFinalization(t)
	source := state.file.GetFullPath()
	_, err := useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	file := state.file
	require.Equal(t, "main.png", file.FileName)
	require.Empty(t, file.URL)
	require.Equal(t, "image/png", file.MimeType)
	require.True(t, file.IsPrimary)
	require.Equal(t, 2, file.GetWidth())
	require.Equal(t, 2, file.GetHeight())
	require.Empty(t, file.GetData().Uploader)
	preset := file.GetData().Presets[shared.FilePresetMainName]
	checksum := sha256.Sum256(download.bodies["main"])
	require.Equal(t, hex.EncodeToString(checksum[:]), preset.ChecksumSHA256)
	require.Equal(t, download.bodies["main"], storage.objects[file.GetFullPath()])
	require.Equal(t, file.GetFullPath(), preset.RelativePath)
	require.Len(t, state.jobs, 2)
	require.Equal(t, "test_job", state.jobs[0].name)
	var after map[string]any
	require.NoError(t, json.Unmarshal([]byte(state.jobs[0].payload), &after))
	metadata, ok := after["meta"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "main.png", metadata["fileName"])
	require.Equal(t, "photo.png", metadata["originalFileName"])
	require.Equal(t, "https://media.example.test/"+file.GetFullPath(), metadata["fileUrl"])
	var cleanup map[string]any
	require.NoError(t, json.Unmarshal([]byte(state.jobs[1].payload), &cleanup))
	require.Equal(t, source, cleanup["filepath"])
	require.Len(t, state.events, 1)
	event, ok := state.events[0].(shared.FileUploadStatusEvent)
	require.True(t, ok)
	require.Equal(t, shared.FileUploadTaskStatusCompleted, event.Status)
	require.Equal(t, "https://media.example.test/"+file.GetFullPath(), event.File.URL)
}

func TestHandle_VideoPresets(t *testing.T) {
	useCase, state, _, download, request := newFinalization(t)
	state.file.FileType = model.FileTypeVideo
	state.file.MimeType = "video/mp4"
	download.bodies["video_mp4_main"] = sampleMP4Header()
	download.bodies["video_mp4_main_thumbnail"] = pngBody(t)
	download.bodies["video_mp4_main_preview"] = pngBody(t)
	request.Artifacts = []uploadfile.Artifact{
		{
			Preset:      "video_mp4_main",
			URL:         "https://resizer.example.test/video",
			MediaType:   "video",
			ContentType: "video/mp4",
			Size:        int64(len(sampleMP4Header())),
			ExpireAt:    time.Now().Add(time.Hour),
			Metadata: map[string]any{
				"target_width":  320,
				"target_height": 180,
			},
		},

		{
			Preset:      "video_mp4_main_thumbnail",
			URL:         "https://resizer.example.test/thumbnail",
			MediaType:   "image",
			ContentType: "image/png",
			ExpireAt:    time.Now().Add(time.Hour),
			Metadata:    map[string]any{"thumbnail": true},
		},

		{
			Preset:      "video_mp4_main_preview",
			URL:         "https://resizer.example.test/preview",
			MediaType:   "image",
			ContentType: "image/png",
			ExpireAt:    time.Now().Add(time.Hour),
			Metadata:    map[string]any{"preview": true},
		},
	}
	_, err := useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "video_mp4_main.mp4", state.file.FileName)
	require.Equal(t, model.FileTypeVideo, state.file.FileType)
	require.Equal(t, 320, state.file.GetWidth())
	require.Equal(t, 180, state.file.GetHeight())
	require.Len(t, state.file.GetData().Presets, 3)
	require.True(t, state.file.GetData().Presets["video_mp4_main_thumbnail"].IsThumbnail)
	require.True(t, state.file.GetData().Presets["video_mp4_main_preview"].IsPreview)
}

func TestHandle_CompletedRecordWithClearedUploaderIsIdempotent(t *testing.T) {
	useCase, state, _, download, request := newFinalization(t)
	_, err := useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	_, err = useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, 1, download.downloads)
	require.Len(t, state.jobs, 2)
	require.Len(t, state.events, 1)
}

func TestHandle_ConcurrentDuplicateIsSerialized(t *testing.T) {
	useCase, state, _, download, request := newFinalization(t)
	failures := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() { defer wait.Done(); _, err := useCase.Handle(context.Background(), request); failures <- err }()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, 1, download.downloads)
	require.Len(t, state.jobs, 2)
	require.Len(t, state.events, 1)
}

func TestHandle_PartialArtifactFailureKeepsFinalsForRetry(t *testing.T) {
	for _, cleanupFlag := range []bool{false, true} {
		t.Run(map[bool]string{false: "retryable", true: "legacy_cleanup_flag"}[cleanupFlag], func(t *testing.T) {
			useCase, state, storage, download, request := newFinalization(t)
			request.CleanupOnFailure = cleanupFlag
			download.failPreset = "thumbnail"
			request.Artifacts = append(request.Artifacts,
				uploadfile.Artifact{
					Preset:      "thumbnail",
					URL:         "https://resizer.example.test/thumbnail",
					MediaType:   "image",
					ContentType: "image/png",
					ExpireAt:    time.Now().Add(time.Hour),
					Metadata:    map[string]any{"thumbnail": true},
				})
			_, err := useCase.Handle(context.Background(), request)
			require.ErrorIs(t, err, errInjected)
			require.Equal(t, shared.FileUploadTaskStatusQueued, state.file.GetData().Uploader.Status)
			require.Empty(t, state.jobs)
			require.Empty(t, state.events)
			require.Len(t, storage.objects, 1)
			require.Zero(t, storage.deletes)
		})
	}
}

func TestHandle_UncertainCommitNeverDeletesFinals(t *testing.T) {
	useCase, state, storage, download, request := newFinalization(t)
	state.uncertainCommit = true
	request.CleanupOnFailure = true
	_, err := useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, errInjected)
	require.Len(t, storage.objects, 1)
	require.Zero(t, storage.deletes)
	require.Len(t, state.jobs, 2)
	_, err = useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, state.jobs, 2)
	require.Equal(t, 1, download.downloads)
}

func TestHandle_ValidationAndFailures(t *testing.T) {
	cases := []struct {
		name   string
		change func(*finalizationState, *finalizationStorage, *finalizationDownload, *uploadfile.Request)
	}{
		{"expired", func(_ *finalizationState, _ *finalizationStorage, _ *finalizationDownload, r *uploadfile.Request) {
			r.Artifacts[0].ExpireAt = time.Now().Add(-time.Minute)
		}},
		{"no_main", func(_ *finalizationState, _ *finalizationStorage, _ *finalizationDownload, r *uploadfile.Request) {
			r.Artifacts[0].Preset = "thumbnail"
			r.Artifacts[0].Metadata = map[string]any{"thumbnail": true}
		}},
		{"duplicate", func(_ *finalizationState, _ *finalizationStorage, _ *finalizationDownload, r *uploadfile.Request) {
			r.Artifacts = append(r.Artifacts, r.Artifacts[0])
		}},
		{"declared_size", func(_ *finalizationState, _ *finalizationStorage, _ *finalizationDownload, r *uploadfile.Request) {
			r.Artifacts[0].Size++
		}},
		{"mime_mismatch", func(_ *finalizationState, _ *finalizationStorage, _ *finalizationDownload, r *uploadfile.Request) {
			r.Artifacts[0].ContentType = "application/pdf"
		}},
		{"nil_reader", func(_ *finalizationState, _ *finalizationStorage, d *finalizationDownload, _ *uploadfile.Request) {
			d.nilBody = true
		}},
		{"download", func(_ *finalizationState, _ *finalizationStorage, d *finalizationDownload, _ *uploadfile.Request) {
			d.failPreset = "main"
		}},
		{"storage", func(_ *finalizationState, s *finalizationStorage, _ *finalizationDownload, _ *uploadfile.Request) {
			s.failSave = true
		}},
		{"metadata", func(s *finalizationState, _ *finalizationStorage, _ *finalizationDownload, _ *uploadfile.Request) {
			s.failUpdate = true
		}},
		{"outbox", func(s *finalizationState, _ *finalizationStorage, _ *finalizationDownload, _ *uploadfile.Request) {
			s.failPut = true
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useCase, state, storage, download, request := newFinalization(t)
			tc.change(state, storage, download, &request)
			_, err := useCase.Handle(context.Background(), request)
			require.Error(t, err)
			require.Equal(t, shared.FileUploadTaskStatusQueued, state.file.GetData().Uploader.Status)
			require.Empty(t, state.jobs)
			require.Empty(t, state.events)
			require.Zero(t, storage.deletes)
		})
	}
}

func TestHandle_CancelledAndDeletedDoNotWrite(t *testing.T) {
	useCase, state, storage, download, request := newFinalization(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := useCase.Handle(ctx, request)
	require.ErrorIs(t, err, context.Canceled)
	state.file = model.File{}
	_, err = useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.Zero(t, download.downloads)
	require.Empty(t, storage.objects)
	require.Empty(t, state.jobs)
}

func TestHandle_ContentScannerRunsBeforePublication(t *testing.T) {
	_, state, storage, download, request := newFinalization(t)
	scanned := false
	scanner := filepolicy.ScannerFunc(func(_ context.Context, reader io.Reader, metadata filepolicy.Metadata) error {
		body, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		require.Equal(t, download.bodies["main"], body)
		require.EqualValues(t, len(body), metadata.Size)
		require.Empty(t, storage.objects)
		scanned = true
		return errInjected
	})
	useCase, err := uploadfile.NewWithContentScanner(uploadfile.NewOptions(state,
		state,
		download,
		state,
		logger.Discard(),
		storage,
		state),
		scanner)
	require.NoError(t, err)
	_, err = useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, errInjected)
	require.True(t, scanned)
	require.Empty(t, storage.objects)
	require.Empty(t, state.jobs)
}

func sampleMP4Header() []byte {
	return []byte{
		0,
		0,
		0,
		32,
		'f',
		't',
		'y',
		'p',
		'i',
		's',
		'o',
		'm',
		0,
		0,
		0,
		0,
		'i',
		's',
		'o',
		'm',
		'i',
		's',
		'o',
		'2',
		0,
		0,
		0,
		8,
		'f',
		'r',
		'e',
		'e',
		0,
		0,
		2,
		212,
		'm',
		'd',
		'a',
		't',
		0,
		0,
		0,
		0,
	}
}

func TestMediaCallbackRejectsAudioBeforeFinalStorage(t *testing.T) {
	useCase, state, storage, download, request := newFinalization(t)
	download.bodies["main"] = audiofixture.MP3
	request.Artifacts[0].ContentType = "audio/mpeg"
	request.Artifacts[0].Size = int64(len(audiofixture.MP3))
	_, err := useCase.Handle(context.Background(), request)
	require.ErrorContains(t, err, "audio artifacts require original_only finalization")
	require.Empty(t, storage.objects)
	require.Empty(t, state.jobs)
	require.Empty(t, state.events)
	require.NotEqual(t, model.FileTypeAudio, state.file.FileType)
}
