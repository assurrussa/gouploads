//go:build integration

//nolint:testpackage // verifies the internal PostgreSQL CAS implementation
package tusupload

import (
	"context"
	"testing"
	"time"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
)

func TestIntegration_PostgresSessionRepositoryFencesCompetingReplicas(t *testing.T) {
	ctx := context.Background()
	database, _, cleanup := testshelpers.PrepareDB(ctx, t, "tusrepository")
	defer cleanup(ctx)

	repoA, err := newPostgresSessionRepository(database)
	require.NoError(t, err)
	repoB, err := newPostgresSessionRepository(database)
	require.NoError(t, err)

	now := time.Now().UTC()
	session := s3Session{
		Session: Session{
			ID:              uuid.NewString(),
			UploadLength:    5,
			Metadata:        map[string]string{"entity_type": "asset"},
			Path:            "quarantine/uploads/test.txt",
			OriginalName:    "test.txt",
			FileName:        "test.txt",
			OwnerUUID:       sharedtypes.NewUserID(),
			CreatedAt:       now,
			UpdatedAt:       now,
			Status:          StatusActive,
			Quarantined:     true,
			FinalizationKey: uuid.NewString(),
		},
		UploadID:  "upload-1",
		Parts:     make(map[int32]durablePart),
		ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, repoA.Create(ctx, session))

	claimed, claim, err := repoA.ClaimAppend(ctx, session.ID, 0, uuid.NewString(), time.Minute, time.Hour)
	require.NoError(t, err)
	require.Positive(t, claim.Fence)
	_, _, err = repoB.ClaimAppend(ctx, session.ID, 0, uuid.NewString(), time.Minute, time.Hour)
	require.ErrorIs(t, err, ErrUploadBusy)

	part := durablePart{Number: 1, Size: 5, ETag: "etag-1", ChecksumSHA256: "checksum"}
	committed, err := repoA.CommitPart(ctx, session.ID, claim, claimed.Offset, part, "text/plain", time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(5), committed.Offset)
	require.Greater(t, committed.Revision, claim.Fence)

	loadedByReplicaB, err := repoB.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, committed.Offset, loadedByReplicaB.Offset)
	require.Equal(t, part, loadedByReplicaB.Parts[1])

	finalizing, finalizeClaim, err := repoB.ClaimFinalize(
		ctx,
		session.ID,
		uuid.NewString(),
		time.Minute,
		time.Hour,
	)
	require.NoError(t, err)
	require.Equal(t, StatusFinalizing, finalizing.Status)
	_, _, err = repoA.ClaimFinalize(ctx, session.ID, uuid.NewString(), time.Minute, time.Hour)
	require.ErrorIs(t, err, ErrUploadBusy)

	ready, err := repoB.CommitFinalize(ctx, session.ID, finalizeClaim)
	require.NoError(t, err)
	require.Equal(t, StatusReady, ready.Status)

	repeated, err := repoA.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, ready.FinalizationKey, repeated.FinalizationKey)

	expired, err := repoA.ListExpired(ctx, ready.ExpiresAt.Add(time.Second), 10)
	require.NoError(t, err)
	require.Contains(t, expired, session.ID, "abandoned ready sessions must remain eligible for staging cleanup")
}
