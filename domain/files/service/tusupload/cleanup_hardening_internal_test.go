package tusupload

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Extend the hand-written test repository with the new fenced cleanup port.
// These methods are not production fallbacks and do not alter generated mocks.
func (r *memorySessionRepository) ClaimCleanup(
	_ context.Context, id string, before time.Time, owner string, ttl time.Duration,
) (cleanupClaim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	if !ok {
		return cleanupClaim{}, ErrNotFound
	}
	if lease := r.leases[id]; lease.until.After(r.clock) {
		return cleanupClaim{}, ErrUploadBusy
	}
	if !before.IsZero() && !session.ExpiresAt.Before(before) {
		return cleanupClaim{}, ErrUploadBusy
	}
	session.Status = StatusCleaning
	session.Revision++
	session.UpdatedAt = r.clock
	r.sessions[id] = session
	r.leases[id] = memoryLease{owner: owner, until: r.clock.Add(ttl)}
	return cleanupClaim{Session: cloneS3Session(session), Claim: sessionClaim{Owner: owner, Fence: session.Revision}}, nil
}

func (r *memorySessionRepository) DeleteClaimed(_ context.Context, id string, claim sessionClaim) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	lease := r.leases[id]
	validFence := session.Revision == claim.Fence && lease.owner == claim.Owner
	if !ok || session.Status != StatusCleaning || !validFence || !lease.until.After(r.clock) {
		return ErrFenceLost
	}
	delete(r.sessions, id)
	delete(r.leases, id)
	return nil
}

func (r *memorySessionRepository) TouchReady(_ context.Context, id string, ttl time.Duration) (s3Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	if !ok {
		return s3Session{}, ErrNotFound
	}
	if session.Status != StatusReady {
		return s3Session{}, ErrUploadBusy
	}
	session.ExpiresAt = maxTime(session.ExpiresAt, r.clock.Add(ttl))
	session.UpdatedAt = r.clock
	r.sessions[id] = session
	return cloneS3Session(session), nil
}

type resumeAfterSelectionRepository struct {
	*memorySessionRepository
	resumeErr error
}

func (r *resumeAfterSelectionRepository) ListExpired(ctx context.Context, before time.Time, limit int) ([]string, error) {
	ids, err := r.memorySessionRepository.ListExpired(ctx, before, limit)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		_, _, r.resumeErr = r.ClaimAppend(ctx, ids[0], 0, uuid.NewString(), defaultLeaseTTL, defaultSessionTTL)
	}
	return ids, nil
}

func TestHardeningS3CleanupDoesNotDeleteResumedSelection(t *testing.T) {
	ctx := context.Background()
	repo := &resumeAfterSelectionRepository{memorySessionRepository: newMemorySessionRepository()}
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	session, err := store.Create(ctx, CreateRequest{UploadLength: 32, OriginalName: "file.pdf", FileName: "file.pdf"})
	require.NoError(t, err)
	repo.mu.Lock()
	current := repo.sessions[session.ID]
	current.ExpiresAt = repo.clock.Add(-time.Second)
	repo.sessions[session.ID] = current
	before := repo.clock.Add(-defaultSessionTTL)
	repo.mu.Unlock()
	count, err := store.Cleanup(ctx, before)
	require.NoError(t, err)
	require.NoError(t, repo.resumeErr)
	require.Zero(t, count)
	current, err = repo.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, StatusActive, current.Status)
	client.mu.Lock()
	defer client.mu.Unlock()
	require.Empty(t, client.abortInputs)
	require.Empty(t, client.deleteInputs)
}

func TestHardeningCleanupClaimBlocksPatchAndRejectsStaleOwner(t *testing.T) {
	ctx := context.Background()
	repo := newMemorySessionRepository()
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	session, err := store.Create(ctx, CreateRequest{UploadLength: 4, OriginalName: "file.pdf", FileName: "file.pdf"})
	require.NoError(t, err)
	claim, err := repo.ClaimCleanup(ctx, session.ID, time.Time{}, uuid.NewString(), defaultLeaseTTL)
	require.NoError(t, err)
	_, err = store.Append(ctx, session.ID, 0, []byte("data"), "application/pdf")
	require.ErrorIs(t, err, ErrUploadBusy)
	repo.advance(2 * defaultLeaseTTL)
	replacement, err := repo.ClaimCleanup(ctx, session.ID, time.Time{}, uuid.NewString(), defaultLeaseTTL)
	require.NoError(t, err)
	require.ErrorIs(t, repo.DeleteClaimed(ctx, session.ID, claim.Claim), ErrFenceLost)
	require.NoError(t, repo.DeleteClaimed(ctx, session.ID, replacement.Claim))
}

type handedOffRepository struct{ *memorySessionRepository }

func (r *handedOffRepository) ClaimCleanup(
	ctx context.Context, id string, before time.Time, owner string, ttl time.Duration,
) (cleanupClaim, error) {
	claim, err := r.memorySessionRepository.ClaimCleanup(ctx, id, before, owner, ttl)
	claim.KeepObject = true
	return claim, err
}

func TestHardeningS3CleanupKeepsHandedOffStaging(t *testing.T) {
	ctx := context.Background()
	repo := &handedOffRepository{newMemorySessionRepository()}
	client := newFakeS3Client()
	store := newTestS3Store(t, client, repo)
	session, err := store.Create(ctx, CreateRequest{UploadLength: 4, OriginalName: "file.pdf", FileName: "file.pdf"})
	require.NoError(t, err)
	_, err = store.Append(ctx, session.ID, 0, []byte("data"), "application/pdf")
	require.NoError(t, err)
	_, err = store.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.NoError(t, store.deleteSession(ctx, session.ID, true))
	_, err = repo.Get(ctx, session.ID)
	require.ErrorIs(t, err, ErrNotFound)
	client.mu.Lock()
	defer client.mu.Unlock()
	require.Empty(t, client.deleteInputs)
}
