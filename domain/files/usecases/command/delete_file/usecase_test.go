package deletefile_test

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sync"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	deletedfile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/events"
	"github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/internal/pointer"
)

var errInjected = errors.New("injected deletion failure")

type (
	deletionTX    struct{}
	deletionState struct {
		mu                                    sync.Mutex
		file                                  model.File
		hidden                                bool
		plan                                  *model.DeletionPlan
		jobs                                  []string
		events                                []shared.FileDeletedEvent
		failRead, failPlan, failHide, failPut bool
		transactions, uncertainAt             int
	}
)

func copyPlan(plan *model.DeletionPlan) *model.DeletionPlan {
	if plan == nil {
		return nil
	}
	body, err := json.Marshal(plan)
	if err != nil {
		panic(err)
	}
	var copied model.DeletionPlan
	if err := json.Unmarshal(body, &copied); err != nil {
		panic(err)
	}
	copied.Completed = plan.Completed
	return &copied
}

func inDeletionTransaction(ctx context.Context) bool {
	active, _ := ctx.Value(deletionTX{}).(bool)
	return active
}

func (s *deletionState) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transactions++
	hidden, plan, jobs := s.hidden, copyPlan(s.plan), append([]string(nil), s.jobs...)
	if err := fn(context.WithValue(ctx, deletionTX{}, true)); err != nil {
		s.hidden, s.plan, s.jobs = hidden, plan, jobs
		return err
	}
	if s.uncertainAt == s.transactions {
		return errInjected
	}
	return nil
}

func (s *deletionState) GetByIDForUpdate(ctx context.Context, _ int64) (model.File, error) {
	if !inDeletionTransaction(ctx) {
		return model.File{}, errors.New("file read outside transaction")
	}
	if s.failRead {
		return model.File{}, errInjected
	}
	if s.hidden {
		return model.File{}, nil
	}
	return s.file, nil
}

func (s *deletionState) DeleteByID(context.Context, int64) error {
	if s.failHide {
		return errInjected
	}
	s.hidden = true
	return nil
}

func (s *deletionState) GetDeletionPlanForUpdate(ctx context.Context, _ int64) (model.DeletionPlan, bool, error) {
	if !inDeletionTransaction(ctx) {
		return model.DeletionPlan{}, false, errors.New("plan read outside transaction")
	}
	if s.plan == nil {
		return model.DeletionPlan{}, false, nil
	}
	return *copyPlan(s.plan), true, nil
}

func (s *deletionState) SaveDeletionPlan(_ context.Context, plan model.DeletionPlan) error {
	if s.failPlan {
		return errInjected
	}
	s.plan = copyPlan(&plan)
	return nil
}

func (s *deletionState) CompleteDeletionPlan(context.Context, int64) error {
	s.plan.Completed = true
	return nil
}

func (s *deletionState) Put(ctx context.Context, _ string, payload string, _ time.Time) (outboxtypes.JobID, error) {
	if !inDeletionTransaction(ctx) {
		return outboxtypes.JobIDNil, errors.New("after job outside transaction")
	}
	if s.failPut {
		return outboxtypes.JobIDNil, errInjected
	}
	s.jobs = append(s.jobs, payload)
	return outboxtypes.NewJobID(), nil
}

func (s *deletionState) Publish(_ context.Context, _ identity.UserID, event events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted, ok := event.(shared.FileDeletedEvent)
	if !ok {
		return errors.New("unexpected deletion event type")
	}
	s.events = append(s.events, deleted)
	return nil
}

type deletionStorage struct {
	mu                                            sync.Mutex
	state                                         *deletionState
	objects                                       map[string]bool
	calls                                         []string
	failOnce, batchUnsupported, deleteUnsupported bool
	stagingOnly                                   bool
}

func (s *deletionStorage) Delete(ctx context.Context, key string) error {
	return s.remove(ctx, []string{key})
}

func (s *deletionStorage) DeleteBatch(ctx context.Context, keys []string) error {
	if s.batchUnsupported {
		return filestorage.ErrNotSupported
	}
	return s.remove(ctx, keys)
}

func (s *deletionStorage) remove(ctx context.Context, keys []string) error {
	if inDeletionTransaction(ctx) {
		return errors.New("storage IO inside database transaction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteUnsupported {
		return filestorage.ErrNotSupported
	}
	if !s.stagingOnly {
		s.state.mu.Lock()
		hidden := s.state.hidden
		plan := s.state.plan != nil
		s.state.mu.Unlock()
		if !hidden || !plan {
			return errors.New("physical deletion before durable intent")
		}
	}
	for _, key := range keys {
		s.calls = append(s.calls, key)
		delete(s.objects, key)
		if s.failOnce {
			s.failOnce = false
			return errInjected
		}
	}
	return nil
}

func newDeletion(t *testing.T) (*deletedfile.UseCase, *deletionState, *deletionStorage, deletedfile.Request) {
	t.Helper()
	user := identity.NewUserID()
	file := model.File{
		ID: 123, FileName: "main.png", OriginalFileName: "original.png", FolderPath: "media/v1/admin/123/slug",
		ObjectType: shared.ObjectTypeAdmin, ObjectID: pointer.To(shared.FileObjectID(123)),
		URL: "https://example.com/media/v1/admin/123/slug/main.png",
		Data: &model.FileData{Presets: map[shared.PresetName]shared.FilePreset{
			"main":     {PresetName: "main", RelativePath: "media/v1/admin/123/slug/main.png"},
			"thumb":    {PresetName: "thumb", RelativePath: "media/v1/admin/123/slug/thumb.png"},
			"original": {PresetName: "original", RelativePath: "media/v1/admin/123/slug/original.png"},
		}},
	}
	state := &deletionState{file: file}
	storage := &deletionStorage{state: state, objects: make(map[string]bool)}
	for _, key := range []string{
		file.GetFullPath(),
		path.Join(file.FolderPath, "thumb.png"),
		path.Join(file.FolderPath, "original.png"),
		path.Join(file.FolderPath, "thumb", file.FileName),
		path.Join(file.FolderPath, "original", file.FileName),
	} {
		storage.objects[key] = true
	}
	useCase, err := deletedfile.New(deletedfile.NewOptions(state, state, state, logger.Discard(), storage, state))
	require.NoError(t, err)
	return useCase, state, storage, deletedfile.Request{
		FileID:      123,
		FilePath:    "tmp/stale-path.png",
		UserID:      user,
		AfterEvents: shared.NewFileEventAfterJobs("model_deleted_bind", user, map[string]any{"foo": "bar"}),
	}
}

func TestHandle_MustInit(t *testing.T) {
	require.Panics(t, func() { deletedfile.Must(deletedfile.NewOptions(nil, nil, nil, nil, nil, nil)) })
}

func TestHandle_SuccessAndPresets(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
	_, err := useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.True(t, state.hidden)
	require.True(t, state.plan.Completed)
	require.Empty(t, storage.objects)
	require.Len(t, storage.calls, 5)
	require.NotContains(t, storage.calls, request.FilePath)
	require.Len(t, state.jobs, 1)
	var job map[string]any
	require.NoError(t, json.Unmarshal([]byte(state.jobs[0]), &job))
	meta, ok := job["meta"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "bar", meta["foo"])
	require.Equal(t, "main.png", meta["fileName"])
	require.Equal(t, "original.png", meta["originalFileName"])
	require.Equal(t, state.file.URL, meta["fileUrl"])
	require.Len(t, state.events, 1)
	require.Equal(t, shared.FileDeleteStatusCompleted, state.events[0].Status)
	_, err = useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, state.jobs, 1)
	require.Len(t, storage.calls, 5)
	require.Len(t, state.events, 1)
}

func TestHandle_RejectsFileOwnedByDifferentObject(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
	request.ObjectType = shared.ObjectTypeAdmin
	request.ObjectID = 999
	_, err := useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, deletedfile.ErrFileOwnershipMismatch)
	require.False(t, state.hidden)
	require.Nil(t, state.plan)
	require.Empty(t, storage.calls)
}

func TestHandle_StorageFailureResumesFromDurablePlan(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
	storage.failOnce = true
	_, err := useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, errInjected)
	require.True(t, state.hidden)
	require.NotNil(t, state.plan)
	require.False(t, state.plan.Completed)
	require.Empty(t, state.jobs)
	require.NotEmpty(t, storage.objects)
	fresh, err := deletedfile.New(deletedfile.NewOptions(state, state, state, logger.Discard(), storage, state))
	require.NoError(t, err)
	_, err = fresh.Handle(context.Background(), request)
	require.NoError(t, err)
	require.True(t, state.plan.Completed)
	require.Empty(t, storage.objects)
	require.Len(t, state.jobs, 1)
}

func TestHandle_DatabaseFailureBeforeIntentDoesNotDeleteStorage(t *testing.T) {
	for _, failure := range []string{"read", "plan", "hide"} {
		t.Run(failure, func(t *testing.T) {
			useCase, state, storage, request := newDeletion(t)
			switch failure {
			case "read":
				state.failRead = true
			case "plan":
				state.failPlan = true
			case "hide":
				state.failHide = true
			}
			_, err := useCase.Handle(context.Background(), request)
			require.ErrorIs(t, err, errInjected)
			require.False(t, state.hidden)
			require.Nil(t, state.plan)
			require.Empty(t, storage.calls)
			require.Len(t, storage.objects, 5)
		})
	}
}

func TestHandle_FailedEnqueueAfterJobsIsRetryable(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
	state.failPut = true
	_, err := useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, errInjected)
	require.True(t, state.hidden)
	require.False(t, state.plan.Completed)
	require.Empty(t, state.jobs)
	require.Empty(t, storage.objects)
	state.failPut = false
	_, err = useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.True(t, state.plan.Completed)
	require.Len(t, state.jobs, 1)
}

func TestHandle_UncertainCommitsDoNotLoseDeletionPlan(t *testing.T) {
	for _, at := range []int{1, 2} {
		t.Run(map[int]string{1: "intent", 2: "completion"}[at], func(t *testing.T) {
			useCase, state, storage, request := newDeletion(t)
			state.uncertainAt = at
			_, err := useCase.Handle(context.Background(), request)
			require.ErrorIs(t, err, errInjected)
			require.True(t, state.hidden)
			require.NotNil(t, state.plan)
			if at == 1 {
				require.Empty(t, storage.calls)
			}
			_, err = useCase.Handle(context.Background(), request)
			require.NoError(t, err)
			require.True(t, state.plan.Completed)
			require.Empty(t, storage.objects)
			require.Len(t, state.jobs, 1)
		})
	}
}

func TestHandle_ConcurrentDeliveryEnqueuesAfterEventOnce(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
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
	require.Len(t, state.jobs, 1)
	require.Empty(t, storage.objects)
	require.True(t, state.plan.Completed)
}

func TestHandle_BatchFallbackAndUnsupportedDelete(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
	storage.batchUnsupported = true
	_, err := useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.True(t, state.plan.Completed)
	useCase, state, storage, request = newDeletion(t)
	storage.batchUnsupported = true
	storage.deleteUnsupported = true
	_, err = useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, filestorage.ErrNotSupported)
	require.False(t, state.plan.Completed)
	require.Empty(t, state.jobs)
}

func TestHandle_FileIDIsEmpty(t *testing.T) {
	useCase, state, storage, request := newDeletion(t)
	storage.stagingOnly = true
	request.FileID = 0
	request.FilePath = "tmp/uploads/source.png"
	_, err := useCase.Handle(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, []string{request.FilePath}, storage.calls)
	require.Zero(t, state.transactions)
	require.Empty(t, state.jobs)
	require.Empty(t, state.events)
	storage.failOnce = true
	_, err = useCase.Handle(context.Background(), request)
	require.ErrorIs(t, err, errInjected)
	request.FilePath = ""
	_, err = useCase.Handle(context.Background(), request)
	require.Error(t, err)
}
