package uploadfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"sync"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	"github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/audiofixture"
	eventstream "github.com/assurrussa/gouploads/internal/events"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

var errOriginalTest = errors.New("original test failure")

type (
	originalTxKey   struct{}
	originalTestJob struct{ name, payload string }
)

// The fake transaction serializes workers and rolls back both the row and jobs.
// It is not a substitute for the real PostgreSQL FOR UPDATE integration gate.
type originalFixture struct {
	mu                                                sync.Mutex
	file                                              model.File
	objects                                           map[string][]byte
	jobs                                              []originalTestJob
	saves, events, deletes                            int
	failSave, failUpdate, failCommit, ambiguousCommit bool
	failPutAt, puts                                   int
}

func cloneOriginalFile(file model.File) model.File {
	if file.Data != nil {
		data := *file.GetData()
		file.SetData(&data)
	}
	return file
}

func (f *originalFixture) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	previous, previousJobs := cloneOriginalFile(f.file), len(f.jobs)
	err := fn(context.WithValue(ctx, originalTxKey{}, true))
	if err == nil && f.failCommit {
		err = errOriginalTest
	}
	if err != nil {
		f.file = previous
		f.jobs = f.jobs[:previousJobs]
		return err
	}
	if f.ambiguousCommit {
		return errOriginalTest // Commit succeeded; only its acknowledgement was lost.
	}
	return nil
}

func (f *originalFixture) GetByID(ctx context.Context, _ int64) (model.File, error) {
	return f.GetByIDForUpdate(ctx, f.file.ID)
}

func (f *originalFixture) GetByIDForUpdate(ctx context.Context, _ int64) (model.File, error) {
	if inTx, _ := ctx.Value(originalTxKey{}).(bool); !inTx {
		return model.File{}, errors.New("read outside transaction")
	}
	return cloneOriginalFile(f.file), nil
}

func (f *originalFixture) Update(ctx context.Context, _ int64, file model.File) error {
	if inTx, _ := ctx.Value(originalTxKey{}).(bool); !inTx || f.failUpdate {
		return errOriginalTest
	}
	f.file = cloneOriginalFile(file)
	return nil
}

func (f *originalFixture) Put(ctx context.Context, name, payload string, _ time.Time) (outboxtypes.JobID, error) {
	f.puts++
	if inTx, _ := ctx.Value(originalTxKey{}).(bool); !inTx || f.failPutAt == f.puts {
		var id outboxtypes.JobID
		return id, errOriginalTest
	}
	f.jobs = append(f.jobs, originalTestJob{name, payload})
	return outboxtypes.NewJobID(), nil
}

func (f *originalFixture) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, errors.New("source not found")
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *originalFixture) SavePersist(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	if err := ctx.Err(); err != nil {
		return filestorage.StoredFile{}, err
	}
	if f.failSave {
		return filestorage.StoredFile{}, errOriginalTest
	}
	body, err := io.ReadAll(input.Reader)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	key := path.Join(input.Dir, input.FileName)
	f.objects[key] = body
	f.saves++
	return filestorage.StoredFile{RelativePath: key, Size: int64(len(body)), MimeType: input.MimeType}, nil
}
func (f *originalFixture) Delete(context.Context, string) error { f.deletes++; return nil }
func (f *originalFixture) Publish(ctx context.Context, _ sharedtypes.UserID, _ eventstream.Event) error {
	if ctx.Value(originalTxKey{}) != nil {
		return errors.New("event published before commit")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events++
	return ctx.Err()
}

func newOriginalFixture(t *testing.T) (*OriginalUseCase, *originalFixture) {
	t.Helper()
	body := []byte("%PDF-1.4\noriginal document bytes\n%%EOF\n")
	objectID := shared.FileObjectID(12)
	file := model.File{
		ID: 21, ObjectType: shared.ObjectTypeAdmin, ObjectID: &objectID,
		Slug:       "75b3f9d5-70a8-4e46-9225-bf8d252b1bbd",
		FolderPath: "tmp/uploads/admin/12/source", FileName: "source.pdf",
		OriginalFileName: "my-document.pdf", MimeType: "application/pdf", Size: int64(len(body)),
		Data: &model.FileData{Uploader: shared.FileUploader{
			UserUUID: sharedtypes.NewUserID(), Type: shared.UserTypeAdmin,
			Status:    shared.FileUploadTaskStatusQueued,
			AfterJobs: []shared.FileEventAfterJob{{JobName: "test_after", Payload: "{}"}},
		}},
	}
	f := &originalFixture{file: file, objects: map[string][]byte{file.GetFullPath(): body}}
	u, err := NewOriginal(OriginalOptions{
		Transaction: f, Repository: f, Storage: f, Outbox: f, Events: f, Logger: logger.Discard(),
	})
	require.NoError(t, err)
	return u, f
}

func TestOriginalFinalizationPreservesBytesAndSchedulesCleanup(t *testing.T) {
	u, f := newOriginalFixture(t)
	source := f.file.GetFullPath()
	original := bytes.Clone(f.objects[source])
	moderation := f.file.Moderate
	require.NoError(t, u.HandleOriginal(t.Context(), f.file.ID))
	require.Equal(t, original, f.objects[f.file.GetFullPath()])
	require.Equal(t, "my-document.pdf", f.file.OriginalFileName)
	require.Equal(t, moderation, f.file.Moderate)
	require.True(t, isCompletedFile(f.file, f.file.GetData().Uploader))
	require.Len(t, f.file.GetData().Presets, 1)
	sum := sha256.Sum256(original)
	require.Equal(t, hex.EncodeToString(sum[:]), f.file.GetData().Presets[shared.FilePresetMainName].ChecksumSHA256)
	require.Len(t, f.jobs, 2)
	require.Equal(t, "test_after", f.jobs[0].name)
	require.Equal(t, deletedfilejob.JobName, f.jobs[1].name)
	cleanup, err := deletedfilejob.UnmarshalPayload(f.jobs[1].payload)
	require.NoError(t, err)
	require.Zero(t, cleanup.FileID)
	require.Contains(t, f.jobs[1].payload, source)
	require.Contains(t, f.objects, source) // A durable job, not premature deletion.
	require.Equal(t, 1, f.events)
	require.NoError(t, u.HandleOriginal(t.Context(), f.file.ID))
	require.Equal(t, 1, f.saves)
	require.Len(t, f.jobs, 2)
	require.Equal(t, 1, f.events)
}

func TestOriginalConcurrentDuplicatesFinalizeOnce(t *testing.T) {
	u, f := newOriginalFixture(t)
	id := f.file.ID
	results := make(chan error, 8)
	for range cap(results) {
		go func() { results <- u.HandleOriginal(t.Context(), id) }()
	}
	for range cap(results) {
		require.NoError(t, <-results)
	}
	require.Equal(t, 1, f.saves)
	require.Equal(t, 1, f.events)
	require.Len(t, f.jobs, 2)
}

func TestOriginalFailureKeepsStagingAndAllowsRetry(t *testing.T) {
	for _, name := range []string{"storage", "metadata", "after_job", "cleanup_job", "commit"} {
		t.Run(name, func(t *testing.T) {
			u, f := newOriginalFixture(t)
			source := f.file.GetFullPath()
			switch name {
			case "storage":
				f.failSave = true
			case "metadata":
				f.failUpdate = true
			case "after_job":
				f.failPutAt = 1
			case "cleanup_job":
				f.failPutAt = 2
			case "commit":
				f.failCommit = true
			}
			require.ErrorIs(t, u.HandleOriginal(t.Context(), f.file.ID), errOriginalTest)
			require.Equal(t, source, f.file.GetFullPath())
			require.Contains(t, f.objects, source)
			require.Empty(t, f.jobs)
			require.Zero(t, f.events)
			require.Zero(t, f.deletes)
			f.failSave = false
			f.failUpdate = false
			f.failCommit = false
			f.failPutAt = 0
			require.NoError(t, u.HandleOriginal(t.Context(), f.file.ID))
			require.True(t, isCompletedFile(f.file, f.file.GetData().Uploader))
		})
	}
}

func TestOriginalAmbiguousCommitNeverDeletesFinalObject(t *testing.T) {
	u, f := newOriginalFixture(t)
	f.ambiguousCommit = true
	require.ErrorIs(t, u.HandleOriginal(t.Context(), f.file.ID), errOriginalTest)
	finalKey := f.file.GetFullPath()
	f.ambiguousCommit = false
	require.NoError(t, u.HandleOriginal(t.Context(), f.file.ID))
	require.Contains(t, f.objects, finalKey)
	require.Equal(t, 1, f.saves)
	require.Zero(t, f.deletes)
	require.Len(t, f.jobs, 2)
}

func TestOriginalRejectsBadMetadataAndPaths(t *testing.T) {
	for _, name := range []string{"mime", "size", "path", "state"} {
		t.Run(name, func(t *testing.T) {
			u, f := newOriginalFixture(t)
			switch name {
			case "mime":
				f.file.MimeType = "image/png"
			case "size":
				f.file.Size++
			case "path":
				f.file.FolderPath = "media/v1/already-public"
			case "state":
				f.file.GetData().Uploader.Status = shared.FileUploadTaskStatusProcessing
			}
			require.Error(t, u.HandleOriginal(t.Context(), f.file.ID))
			require.Empty(t, f.jobs)
			require.Zero(t, f.events)
		})
	}
}

func TestOriginalReplacementIsEnqueuedOnlyAfterSuccess(t *testing.T) {
	u, f := newOriginalFixture(t)
	userID := f.file.GetData().Uploader.UserUUID
	payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewOwnedPayload(
		9, userID, f.file.ObjectType, *f.file.ObjectID, "",
	))
	require.NoError(t, err)
	f.file.GetData().Uploader.AfterJobs = []shared.FileEventAfterJob{{JobName: deletedfilejob.JobName, Payload: payload}}
	f.failUpdate = true
	require.Error(t, u.HandleOriginal(t.Context(), f.file.ID))
	require.Empty(t, f.jobs)
	f.failUpdate = false
	require.NoError(t, u.HandleOriginal(t.Context(), f.file.ID))
	require.Len(t, f.jobs, 2)
	require.Equal(t, payload, f.jobs[0].payload)
	require.Zero(t, f.deletes)
}

func TestOriginalDeletedFileIsNotResurrected(t *testing.T) {
	u, f := newOriginalFixture(t)
	f.file = model.File{}
	require.NoError(t, u.HandleOriginal(t.Context(), 21))
	require.Zero(t, f.saves)
	require.Empty(t, f.jobs)
}

func TestOriginalLargeID3FrameValidationBeforePublication(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "valid frame", false: "forged frame"}[valid], func(t *testing.T) {
			u, f := newOriginalFixture(t)
			body := audiofixture.MP3WithTag(4096)
			if !valid {
				copy(body[4106:], "junk")
			}
			f.file.FileName = "source.mp3"
			f.file.OriginalFileName = "tagged.mp3"
			f.file.MimeType = "audio/mpeg"
			f.file.FileType = model.FileTypeAudio
			f.file.Size = int64(len(body))
			f.objects[f.file.GetFullPath()] = body
			err := u.HandleOriginal(t.Context(), f.file.ID)
			if !valid {
				require.ErrorContains(t, err, "invalid or truncated MPEG Layer III frame")
				require.Zero(t, f.saves)
				require.Empty(t, f.jobs)
				require.Zero(t, f.events)
				return
			}
			require.NoError(t, err)
			require.Equal(t, body, f.objects[f.file.GetFullPath()])
			sum := sha256.Sum256(body)
			require.Equal(t, hex.EncodeToString(sum[:]), f.file.GetData().Presets[shared.FilePresetMainName].ChecksumSHA256)
		})
	}
}
