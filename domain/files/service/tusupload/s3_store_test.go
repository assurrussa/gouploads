//nolint:testpackage // exercises the internal durable state machine
package tusupload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
)

func TestS3Store_DurableCrossReplicaResumeAndIdempotentFinalize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	storeA := newTestS3Store(t, client, repo)
	storeB := newTestS3Store(t, client, repo)

	firstChunk := make([]byte, s3store.MinPartSize)
	lastChunk := []byte("tail")
	session, err := storeA.Create(ctx, CreateRequest{
		UploadLength: int64(len(firstChunk) + len(lastChunk)),
		OriginalName: "test.png",
		FileName:     "test.png",
		Metadata: map[string]string{
			"entity_type": "exercise",
			"entity_id":   "12",
		},
	})
	require.NoError(t, err)
	require.True(t, session.Quarantined)
	require.Equal(t, StatusActive, session.Status)
	require.NotEmpty(t, session.FinalizationKey)
	require.Empty(t, session.URL)

	offset, err := storeA.Append(ctx, session.ID, 0, firstChunk, "image/png")
	require.NoError(t, err)
	require.Equal(t, int64(len(firstChunk)), offset)

	offset, err = storeB.Append(ctx, session.ID, offset, lastChunk, "image/png")
	require.NoError(t, err)
	require.Equal(t, session.UploadLength, offset)

	result, err := storeB.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, session.UploadLength, result.Size)
	require.Equal(t, session.FinalizationKey, result.FinalizationKey)
	require.True(t, result.Quarantined)
	require.Equal(t, "staging/v1/tus/"+session.ID+"/source.png", result.RelativePath)
	require.Empty(t, result.URL)

	repeated, err := storeA.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, result, repeated)

	client.mu.Lock()
	require.Len(t, client.createInputs, 1)
	require.Empty(t, client.createInputs[0].ACL)
	require.Equal(t, "staging-bucket", aws.ToString(client.createInputs[0].Bucket))
	require.Equal(t, "private,no-store", aws.ToString(client.createInputs[0].CacheControl))
	require.Equal(t, awss3types.ChecksumAlgorithmSha256, client.createInputs[0].ChecksumAlgorithm)
	require.Len(t, client.uploadInputs, 2)
	require.Len(t, client.completeInputs, 1)
	client.mu.Unlock()
}

func TestS3Store_FinalizesWhenListPartsOmitsChecksum(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	client.omitListPartChecksums = true
	store := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "test.txt",
		FileName:     "test.txt",
	})
	require.NoError(t, err)

	_, err = store.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)

	result, err := store.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, session.UploadLength, result.Size)

	client.mu.Lock()
	require.Len(t, client.completeInputs, 1)
	require.Nil(t, client.completeInputs[0].MultipartUpload.Parts[0].ChecksumSHA256)
	client.mu.Unlock()
}

func TestS3Store_DeleteReadyKeepsCompletedStagingObjectForHandoff(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "source.txt",
		FileName:     "source.txt",
	})
	require.NoError(t, err)
	_, err = store.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)
	_, err = store.Complete(ctx, session.ID)
	require.NoError(t, err)

	require.NoError(t, store.Delete(ctx, session.ID))
	_, err = repo.Get(ctx, session.ID)
	require.ErrorIs(t, err, ErrNotFound)

	client.mu.Lock()
	require.Empty(t, client.deleteInputs)
	client.mu.Unlock()
}

func TestS3Store_CleanupRemovesExpiredReadyStagingObject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "source.txt",
		FileName:     "source.txt",
	})
	require.NoError(t, err)
	_, err = store.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)
	_, err = store.Complete(ctx, session.ID)
	require.NoError(t, err)

	removed, err := store.Cleanup(ctx, session.CreatedAt.Add(-time.Nanosecond))
	require.NoError(t, err)
	require.Zero(t, removed, "cleanup must not remove a session before its single TTL has elapsed")

	removed, err = store.Cleanup(ctx, session.CreatedAt.Add(time.Nanosecond))
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = repo.Get(ctx, session.ID)
	require.ErrorIs(t, err, ErrNotFound)

	client.mu.Lock()
	require.Len(t, client.deleteInputs, 1)
	require.Equal(t, "staging-bucket", aws.ToString(client.deleteInputs[0].Bucket))
	require.Equal(t, "staging/v1/tus/"+session.ID+"/source.txt", aws.ToString(client.deleteInputs[0].Key))
	client.mu.Unlock()
}

func TestS3Store_ConcurrentPatchUsesSingleFence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	client.uploadStarted = make(chan struct{}, 1)
	client.releaseUpload = make(chan struct{})
	storeA := newTestS3Store(t, client, repo)
	storeB := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := storeA.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "test.txt",
		FileName:     "test.txt",
	})
	require.NoError(t, err)

	firstDone := make(chan error, 1)
	go func() {
		_, appendErr := storeA.Append(ctx, session.ID, 0, payload, "text/plain")
		firstDone <- appendErr
	}()

	select {
	case <-client.uploadStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	_, err = storeB.Append(ctx, session.ID, 0, payload, "text/plain")
	require.ErrorIs(t, err, ErrUploadBusy)
	close(client.releaseUpload)
	require.NoError(t, <-firstDone)

	client.mu.Lock()
	require.Len(t, client.uploadInputs, 1)
	client.mu.Unlock()
}

func TestS3Store_ReconcilesCrashAfterPartBeforeMetadataCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	repo.failNextPartCommit = true
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "test.txt",
		FileName:     "test.txt",
	})
	require.NoError(t, err)

	_, err = store.Append(ctx, session.ID, 0, payload, "text/plain")
	require.ErrorContains(t, err, "injected commit failure")

	repo.advance(2 * defaultLeaseTTL)
	offset, err := store.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), offset)

	client.mu.Lock()
	require.Len(t, client.uploadInputs, 1, "the matching S3 part must be reused")
	client.mu.Unlock()
}

func TestS3Store_CompetingFinalizeIsFencedAndRetryIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	storeA := newTestS3Store(t, client, repo)
	storeB := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := storeA.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "test.txt",
		FileName:     "test.txt",
	})
	require.NoError(t, err)
	_, err = storeA.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)

	client.completeStarted = make(chan struct{}, 1)
	client.releaseComplete = make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, completeErr := storeA.Complete(ctx, session.ID)
		firstDone <- completeErr
	}()
	<-client.completeStarted

	_, err = storeB.Complete(ctx, session.ID)
	require.ErrorIs(t, err, ErrUploadBusy)
	close(client.releaseComplete)
	require.NoError(t, <-firstDone)

	result, err := storeB.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, session.FinalizationKey, result.FinalizationKey)

	client.mu.Lock()
	require.Len(t, client.completeInputs, 1)
	client.mu.Unlock()
}

func TestS3Store_RecoversCrashAfterS3FinalizeBeforeMetadataCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	repo.failNextFinalizeCommit = true
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	payload := []byte("complete payload")

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "test.txt",
		FileName:     "test.txt",
	})
	require.NoError(t, err)
	_, err = store.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)

	_, err = store.Complete(ctx, session.ID)
	require.ErrorContains(t, err, "injected finalize commit failure")
	repo.advance(2 * defaultLeaseTTL)

	recovered, err := store.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, session.FinalizationKey, recovered.FinalizationKey)

	client.mu.Lock()
	require.Len(t, client.completeInputs, 1, "S3 completion must not be repeated after recovery")
	client.mu.Unlock()
}

func TestS3Store_CleanupAbortsExpiredQuarantine(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	session, err := store.Create(ctx, CreateRequest{
		UploadLength: 5,
		OriginalName: "test.txt",
		FileName:     "test.txt",
	})
	require.NoError(t, err)

	removed, err := store.Cleanup(ctx, session.CreatedAt.Add(2*defaultSessionTTL))
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = store.Get(ctx, session.ID)
	require.ErrorIs(t, err, ErrNotFound)

	client.mu.Lock()
	require.Len(t, client.abortInputs, 1)
	client.mu.Unlock()
}

func TestS3Store_CleanupAbortsMultipartWithoutDurableSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	created, err := client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String("bucket"),
		Key:    aws.String(defaultS3StorePrefix + "/orphan.txt"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, aws.ToString(created.UploadId))

	removed, err := store.Cleanup(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	client.mu.Lock()
	require.Len(t, client.abortInputs, 1)
	client.mu.Unlock()
}

func TestS3Store_RejectsSmallIntermediateChunk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestS3Store(t, newFakeS3Client(), newMemorySessionRepository())
	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(s3store.MinPartSize) + 1,
		OriginalName: "test.png",
		FileName:     "test.png",
	})
	require.NoError(t, err)

	_, err = store.Append(ctx, session.ID, 0, []byte("small"), "image/png")
	require.ErrorIs(t, err, ErrChunkTooSmall)
}

func newTestS3Store(t *testing.T, client s3Client, repo sessionRepository) *S3Store {
	t.Helper()
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	store, err := NewS3Store(client, domain, repo, S3StoreConfig{
		Prefix:   defaultS3StorePrefix,
		PartSize: s3store.MinPartSize,
		TTL:      defaultSessionTTL,
		LeaseTTL: defaultLeaseTTL,
	})
	require.NoError(t, err)
	return store
}

type memorySessionRepository struct {
	mu                     sync.Mutex
	clock                  time.Time
	sessions               map[string]s3Session
	leases                 map[string]memoryLease
	failNextPartCommit     bool
	failNextFinalizeCommit bool
}

type memoryLease struct {
	owner string
	until time.Time
}

func newMemorySessionRepository() *memorySessionRepository {
	return &memorySessionRepository{
		clock:    time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		sessions: make(map[string]s3Session),
		leases:   make(map[string]memoryLease),
	}
}

func (r *memorySessionRepository) advance(duration time.Duration) {
	r.mu.Lock()
	r.clock = r.clock.Add(duration)
	r.mu.Unlock()
}

func (r *memorySessionRepository) Create(_ context.Context, session s3Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[session.ID]; exists {
		return errors.New("duplicate session")
	}
	r.sessions[session.ID] = cloneS3Session(session)
	return nil
}

func (r *memorySessionRepository) Get(_ context.Context, id string) (s3Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[id]
	if !exists {
		return s3Session{}, ErrNotFound
	}
	return cloneS3Session(session), nil
}

func (r *memorySessionRepository) ClaimAppend(
	_ context.Context,
	id string,
	expectedOffset int64,
	owner string,
	leaseTTL time.Duration,
	sessionTTL time.Duration,
) (s3Session, sessionClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[id]
	if !exists {
		return s3Session{}, sessionClaim{}, ErrNotFound
	}
	if session.Status == StatusReady {
		return session, sessionClaim{}, ErrUploadFinalized
	}
	if session.Status != StatusActive {
		return session, sessionClaim{}, ErrUploadBusy
	}
	if session.Offset != expectedOffset {
		return session, sessionClaim{}, ErrOffsetMismatch
	}
	if lease, leased := r.leases[id]; leased && lease.until.After(r.clock) && lease.owner != owner {
		return session, sessionClaim{}, ErrUploadBusy
	}
	session.Revision++
	session.UpdatedAt = r.clock
	session.ExpiresAt = maxTime(session.ExpiresAt, r.clock.Add(sessionTTL))
	r.sessions[id] = session
	r.leases[id] = memoryLease{owner: owner, until: r.clock.Add(leaseTTL)}
	return cloneS3Session(session), sessionClaim{Owner: owner, Fence: session.Revision}, nil
}

func (r *memorySessionRepository) CommitPart(
	_ context.Context,
	id string,
	claim sessionClaim,
	expectedOffset int64,
	part durablePart,
	mimeType string,
	sessionTTL time.Duration,
) (s3Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNextPartCommit {
		r.failNextPartCommit = false
		return s3Session{}, errors.New("injected commit failure")
	}
	session, exists := r.sessions[id]
	lease := r.leases[id]
	if !exists || session.Revision != claim.Fence || lease.owner != claim.Owner ||
		!lease.until.After(r.clock) || session.Offset != expectedOffset {
		return s3Session{}, ErrFenceLost
	}
	session.Parts[part.Number] = part
	session.Offset += part.Size
	if session.MimeType == "" {
		session.MimeType = mimeType
	}
	session.Revision++
	session.UpdatedAt = r.clock
	session.ExpiresAt = maxTime(session.ExpiresAt, r.clock.Add(sessionTTL))
	r.sessions[id] = session
	delete(r.leases, id)
	return cloneS3Session(session), nil
}

func (r *memorySessionRepository) ClaimFinalize(
	_ context.Context,
	id string,
	owner string,
	leaseTTL time.Duration,
	sessionTTL time.Duration,
) (s3Session, sessionClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[id]
	if !exists {
		return s3Session{}, sessionClaim{}, ErrNotFound
	}
	if session.Status == StatusReady {
		return session, sessionClaim{}, ErrUploadFinalized
	}
	if session.Offset != session.UploadLength {
		return session, sessionClaim{}, ErrOffsetMismatch
	}
	if lease, leased := r.leases[id]; leased && lease.until.After(r.clock) && lease.owner != owner {
		return session, sessionClaim{}, ErrUploadBusy
	}
	session.Status = StatusFinalizing
	session.Revision++
	session.UpdatedAt = r.clock
	session.ExpiresAt = maxTime(session.ExpiresAt, r.clock.Add(sessionTTL))
	r.sessions[id] = session
	r.leases[id] = memoryLease{owner: owner, until: r.clock.Add(leaseTTL)}
	return cloneS3Session(session), sessionClaim{Owner: owner, Fence: session.Revision}, nil
}

func (r *memorySessionRepository) CommitFinalize(
	_ context.Context,
	id string,
	claim sessionClaim,
) (s3Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failNextFinalizeCommit {
		r.failNextFinalizeCommit = false
		return s3Session{}, errors.New("injected finalize commit failure")
	}
	session, exists := r.sessions[id]
	lease := r.leases[id]
	if !exists || session.Revision != claim.Fence || lease.owner != claim.Owner || !lease.until.After(r.clock) {
		return s3Session{}, ErrFenceLost
	}
	session.Status = StatusReady
	session.Revision++
	session.UpdatedAt = r.clock
	r.sessions[id] = session
	delete(r.leases, id)
	return cloneS3Session(session), nil
}

func (r *memorySessionRepository) Release(_ context.Context, id string, claim sessionClaim) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[id]
	lease := r.leases[id]
	if exists && session.Revision == claim.Fence && lease.owner == claim.Owner {
		session.Revision++
		r.sessions[id] = session
		delete(r.leases, id)
	}
	return nil
}

func (r *memorySessionRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[id]; !exists {
		return ErrNotFound
	}
	delete(r.sessions, id)
	delete(r.leases, id)
	return nil
}

func (r *memorySessionRepository) ListExpired(
	_ context.Context,
	before time.Time,
	limit int,
) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, limit)
	for id, session := range r.sessions {
		lease := r.leases[id]
		if session.ExpiresAt.Before(before) && !lease.until.After(r.clock) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (r *memorySessionRepository) HasMultipart(
	_ context.Context,
	objectPath string,
	uploadID string,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, session := range r.sessions {
		if session.Status != StatusReady && session.Path == objectPath && session.UploadID == uploadID {
			return true, nil
		}
	}
	return false, nil
}

func cloneS3Session(session s3Session) s3Session {
	cloned := session
	cloned.Metadata = cloneMetadata(session.Metadata)
	cloned.Parts = make(map[int32]durablePart, len(session.Parts))
	for number, part := range session.Parts {
		cloned.Parts[number] = part
	}
	return cloned
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}

type fakeS3Client struct {
	mu                    sync.Mutex
	nextUploadID          int
	createInputs          []s3.CreateMultipartUploadInput
	uploadInputs          []s3.UploadPartInput
	completeInputs        []s3.CompleteMultipartUploadInput
	abortInputs           []s3.AbortMultipartUploadInput
	deleteInputs          []s3.DeleteObjectInput
	uploads               map[string]*fakeMultipartUpload
	uploadStarted         chan struct{}
	releaseUpload         chan struct{}
	completeStarted       chan struct{}
	releaseComplete       chan struct{}
	omitListPartChecksums bool
}

type fakeMultipartUpload struct {
	key       string
	initiated time.Time
	parts     map[int32]awss3types.Part
	completed bool
	size      int64
}

func newFakeS3Client() *fakeS3Client {
	return &fakeS3Client{uploads: make(map[string]*fakeMultipartUpload)}
}

func (f *fakeS3Client) CreateMultipartUpload(
	_ context.Context,
	input *s3.CreateMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.CreateMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextUploadID++
	uploadID := fmt.Sprintf("upload-%d", f.nextUploadID)
	f.createInputs = append(f.createInputs, *input)
	f.uploads[uploadID] = &fakeMultipartUpload{
		key:       aws.ToString(input.Key),
		initiated: time.Now(),
		parts:     make(map[int32]awss3types.Part),
	}
	return &s3.CreateMultipartUploadOutput{UploadId: aws.String(uploadID)}, nil
}

func (f *fakeS3Client) AbortMultipartUpload(
	_ context.Context,
	input *s3.AbortMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.AbortMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abortInputs = append(f.abortInputs, *input)
	delete(f.uploads, aws.ToString(input.UploadId))
	return &s3.AbortMultipartUploadOutput{}, nil
}

func (f *fakeS3Client) UploadPart(
	ctx context.Context,
	input *s3.UploadPartInput,
	_ ...func(*s3.Options),
) (*s3.UploadPartOutput, error) {
	if f.uploadStarted != nil {
		select {
		case f.uploadStarted <- struct{}{}:
		default:
		}
	}
	if f.releaseUpload != nil {
		select {
		case <-f.releaseUpload:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	upload, exists := f.uploads[aws.ToString(input.UploadId)]
	if !exists || upload.completed {
		return nil, errors.New("multipart upload not found")
	}
	partNumber := aws.ToInt32(input.PartNumber)
	etag := fmt.Sprintf("etag-%d-%s", partNumber, aws.ToString(input.ChecksumSHA256))
	part := awss3types.Part{
		PartNumber:     aws.Int32(partNumber),
		Size:           aws.Int64(int64(len(body))),
		ETag:           aws.String(etag),
		ChecksumSHA256: input.ChecksumSHA256,
	}
	upload.parts[partNumber] = part
	f.uploadInputs = append(f.uploadInputs, *input)
	return &s3.UploadPartOutput{ETag: aws.String(etag), ChecksumSHA256: input.ChecksumSHA256}, nil
}

func (f *fakeS3Client) ListParts(
	_ context.Context,
	input *s3.ListPartsInput,
	_ ...func(*s3.Options),
) (*s3.ListPartsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	upload, exists := f.uploads[aws.ToString(input.UploadId)]
	if !exists || upload.completed {
		return nil, errors.New("multipart upload not found")
	}
	marker, _ := strconv.Atoi(aws.ToString(input.PartNumberMarker))
	numbers := make([]int, 0, len(upload.parts))
	for number := range upload.parts {
		if int(number) > marker {
			numbers = append(numbers, int(number))
		}
	}
	sort.Ints(numbers)
	limit := len(numbers)
	if input.MaxParts != nil && int(aws.ToInt32(input.MaxParts)) < limit {
		limit = int(aws.ToInt32(input.MaxParts))
	}
	parts := make([]awss3types.Part, 0, limit)
	for _, number := range numbers[:limit] {
		part := upload.parts[int32(number)]
		if f.omitListPartChecksums {
			part.ChecksumSHA256 = nil
		}
		parts = append(parts, part)
	}
	result := &s3.ListPartsOutput{Parts: parts, IsTruncated: aws.Bool(limit < len(numbers))}
	if limit > 0 && limit < len(numbers) {
		result.NextPartNumberMarker = aws.String(strconv.Itoa(numbers[limit-1]))
	}
	return result, nil
}

func (f *fakeS3Client) ListMultipartUploads(
	_ context.Context,
	input *s3.ListMultipartUploadsInput,
	_ ...func(*s3.Options),
) (*s3.ListMultipartUploadsOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	uploads := make([]awss3types.MultipartUpload, 0, len(f.uploads))
	for uploadID, upload := range f.uploads {
		if upload.completed || !strings.HasPrefix(upload.key, aws.ToString(input.Prefix)) {
			continue
		}
		uploads = append(uploads, awss3types.MultipartUpload{
			Initiated: aws.Time(upload.initiated),
			Key:       aws.String(upload.key),
			UploadId:  aws.String(uploadID),
		})
	}
	sort.Slice(uploads, func(i, j int) bool {
		if aws.ToString(uploads[i].Key) == aws.ToString(uploads[j].Key) {
			return aws.ToString(uploads[i].UploadId) < aws.ToString(uploads[j].UploadId)
		}
		return aws.ToString(uploads[i].Key) < aws.ToString(uploads[j].Key)
	})
	return &s3.ListMultipartUploadsOutput{Uploads: uploads, IsTruncated: aws.Bool(false)}, nil
}

func (f *fakeS3Client) CompleteMultipartUpload(
	ctx context.Context,
	input *s3.CompleteMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.CompleteMultipartUploadOutput, error) {
	if f.completeStarted != nil {
		select {
		case f.completeStarted <- struct{}{}:
		default:
		}
	}
	if f.releaseComplete != nil {
		select {
		case <-f.releaseComplete:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	upload, exists := f.uploads[aws.ToString(input.UploadId)]
	if !exists || upload.completed {
		return nil, errors.New("multipart upload not found")
	}
	for _, part := range upload.parts {
		upload.size += aws.ToInt64(part.Size)
	}
	upload.completed = true
	f.completeInputs = append(f.completeInputs, *input)
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (f *fakeS3Client) HeadObject(
	_ context.Context,
	input *s3.HeadObjectInput,
	_ ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, upload := range f.uploads {
		if upload.key == aws.ToString(input.Key) && upload.completed {
			return &s3.HeadObjectOutput{ContentLength: aws.Int64(upload.size)}, nil
		}
	}
	return nil, errors.New("object not found")
}

func (f *fakeS3Client) DeleteObject(
	_ context.Context,
	input *s3.DeleteObjectInput,
	_ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteInputs = append(f.deleteInputs, *input)
	for uploadID, upload := range f.uploads {
		if upload.key == aws.ToString(input.Key) && upload.completed {
			delete(f.uploads, uploadID)
		}
	}
	return &s3.DeleteObjectOutput{}, nil
}

var (
	_ sessionRepository = (*memorySessionRepository)(nil)
	_ s3Client          = (*fakeS3Client)(nil)
)
