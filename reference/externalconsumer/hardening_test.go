package externalconsumer_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestUploadPolicyPublicConstructor(t *testing.T) {
	actor := host.UploadContext{UserID: 23}
	denied := errors.New("object access denied")
	policy := host.UploadHandlerPolicy{
		AllowDefaultStrategy: true,
		AllowAnonymousTUS:    false,
		Authorize: func(_ context.Context, got host.UploadContext, action string, file host.File) error {
			require.Equal(t, actor.UserID, got.UserID)
			require.Equal(t, "upload", action)
			require.EqualValues(t, 42, file.ID)
			return denied
		},
		ResolveActor: func(_ context.Context, got host.UploadContext) (int64, int64, error) {
			return 0, got.UserID, nil
		},
	}
	handler := host.NewUploadHandlerWithPolicy(nil, nil, nil, nil, nil, nil, policy)
	require.NotNil(t, handler)
	require.ErrorIs(t, policy.Authorize(t.Context(), actor, "upload", host.File{ID: 42}), denied)
	managerID, userID, err := policy.ResolveActor(t.Context(), actor)
	require.NoError(t, err)
	require.Zero(t, managerID)
	require.EqualValues(t, 23, userID)
}

func TestContentScannerPublicContract(t *testing.T) {
	denied := errors.New("scanner rejected content")
	metadata := host.ContentScanMetadata{OriginalName: "source.pdf", ContentType: "application/pdf", Size: 7}
	var scanner host.ContentScanner = host.ContentScannerFunc(
		func(ctx context.Context, reader io.Reader, got host.ContentScanMetadata) error {
			require.Equal(t, t.Context(), ctx)
			require.Equal(t, metadata, got)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, "%PDF-1\n", string(body))
			return denied
		},
	)
	deps := host.OriginalRuntimeDeps{ContentScanner: scanner}
	require.ErrorIs(t, deps.ContentScanner.Scan(t.Context(), strings.NewReader("%PDF-1\n"), metadata), denied)
	// Missing dependencies are checked before any connection or file access.
	runtime, err := host.NewOriginalRuntime(host.StorageConfig{}, deps)
	require.Error(t, err)
	require.Nil(t, runtime)
}

func TestFinalizationAndBatchPublicErrors(t *testing.T) {
	req := host.ReaderRequest{FinalizationKey: "server-issued-key"}
	completion := host.TusCompleteResult{FinalizationKey: req.FinalizationKey, Quarantined: true}
	require.Equal(t, req.FinalizationKey, completion.FinalizationKey)
	for _, cause := range []error{
		host.ErrFinalizationConflict, host.ErrFinalizationGone, host.ErrObjectBindingMismatch,
	} {
		err := fmt.Errorf("batch: %w", &host.BatchError{FailedIndex: 2, Err: cause})
		var batchErr *host.BatchError
		require.ErrorAs(t, err, &batchErr)
		require.Equal(t, 2, batchErr.FailedIndex)
		require.ErrorIs(t, err, cause)
	}
}

type chunkSizeStore struct {
	host.TusStore
	chunkSize int64
}

func (s chunkSizeStore) ChunkSize() int64 { return s.chunkSize }

func TestStandardUploadHandlerPublicContract(t *testing.T) {
	uploadHandler := host.NewUploadHandler(nil, nil, nil, nil, nil, nil)
	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files", host.StandardUploadHandlerConfig{
		BodyLimit: 50 * 1024 * 1024,
	})
	require.NoError(t, err)
	require.NotNil(t, stdHandler)
	oversizedHandler := host.NewUploadHandler(nil, nil,
		chunkSizeStore{chunkSize: host.DefaultBodyLimit + 1}, nil, nil, nil)
	stdHandler, err = host.NewStandardUploadHandler(oversizedHandler, "/files")
	require.Nil(t, stdHandler)
	require.ErrorIs(t, err, host.ErrIncompatibleBodyLimit)

	stdHandler, err = host.NewStandardUploadHandler(nil, "/files")
	require.Nil(t, stdHandler)
	require.ErrorIs(t, err, host.ErrNilUploadHandler)

	stdHandler, err = host.NewStandardUploadHandler(&host.UploadHandler{}, "/files")
	require.Nil(t, stdHandler)
	require.ErrorIs(t, err, host.ErrNilUploadHandler)

	stdHandler, err = host.NewStandardUploadHandler(uploadHandler, "/files",
		host.StandardUploadHandlerConfig{}, host.StandardUploadHandlerConfig{})
	require.Nil(t, stdHandler)
	require.ErrorIs(t, err, host.ErrInvalidStandardHandlerConfig)
}
