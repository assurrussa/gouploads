package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pgsqlmocks "github.com/assurrussa/outbox/backends/pgsql/storage/mocks"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/host"
)

type callerOwnedStorage struct {
	host.Storage
	closed int
}

func (s *callerOwnedStorage) Close() error {
	s.closed++
	return nil
}

func originalStorageDeps(t *testing.T) host.OriginalRuntimeDeps {
	t.Helper()
	ctrl := gomock.NewController(t)
	return host.OriginalRuntimeDeps{
		Database: pgsqlmocks.NewMockClient(ctrl), Transaction: pgsqlmocks.NewMockTxManager(ctrl),
		Outbox: &originalStorageOutbox{},
	}
}

func TestOriginalRuntimeDefaultAndInjectedStorage(t *testing.T) {
	t.Parallel()
	for _, injected := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "caller owned"}[injected], func(t *testing.T) {
			t.Parallel()
			cfg := host.StorageConfig{Local: host.StorageLocalConfig{Root: t.TempDir()}}
			deps := originalStorageDeps(t)
			var supplied *callerOwnedStorage
			if injected {
				base, err := host.NewStorage(cfg)
				require.NoError(t, err)
				supplied = &callerOwnedStorage{Storage: base}
				deps.Storage = supplied
			}
			runtime, err := host.NewOriginalRuntime(cfg, deps)
			require.NoError(t, err)
			require.NotNil(t, runtime.Storage)
			require.NotNil(t, runtime.TusStore)
			require.NotNil(t, runtime.Finalizer)
			require.Len(t, runtime.Jobs, 2)
			if injected {
				require.Same(t, supplied, runtime.Storage)
				require.Zero(t, supplied.closed)
			}
		})
	}
}

func TestOriginalRuntimeStorageOverrideRejectsIncompatibleConfiguration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*host.StorageConfig)
	}{
		{"missing local root", func(cfg *host.StorageConfig) { cfg.Local.Root = "" }},
		{"unsupported driver", func(cfg *host.StorageConfig) { cfg.Driver = "external" }},
		{"media mode", func(cfg *host.StorageConfig) { cfg.ProcessingMode = host.ProcessingMediaResizer }},
		{"invalid destination", func(cfg *host.StorageConfig) { cfg.Public.Prefix = "../escape" }},
		{"staging overlap", func(cfg *host.StorageConfig) { cfg.Public.Prefix = "tmp/uploads" }},
		{"invalid local source URL", func(cfg *host.StorageConfig) { cfg.Local.SourceBaseURL = "file:///private" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := host.StorageConfig{Local: host.StorageLocalConfig{Root: filepath.Join(t.TempDir(), "uncreated")}}
			tc.mutate(&cfg)
			supplied := &callerOwnedStorage{}
			deps := originalStorageDeps(t)
			deps.Storage = supplied
			runtime, err := host.NewOriginalRuntime(cfg, deps)
			require.Error(t, err)
			require.Nil(t, runtime)
			require.Zero(t, supplied.closed)
			if cfg.Local.Root != "" {
				_, err = os.Stat(cfg.Local.Root)
				require.ErrorIs(t, err, os.ErrNotExist, "reject before creating the TUS spool")
			}
		})
	}
}

func TestOriginalRuntimeStorageOverrideRejectsS3AndTypedNil(t *testing.T) {
	t.Parallel()
	deps := originalStorageDeps(t)
	deps.Storage = &callerOwnedStorage{}
	runtime, err := host.NewOriginalRuntime(host.StorageConfig{Driver: host.StorageDriverS3}, deps)
	require.ErrorIs(t, err, host.ErrOriginalStorageRequiresLocalTus)
	require.Nil(t, runtime)
	var typedNil *callerOwnedStorage
	deps.Storage = typedNil
	runtime, err = host.NewOriginalRuntime(host.StorageConfig{Local: host.StorageLocalConfig{Root: t.TempDir()}}, deps)
	require.ErrorIs(t, err, host.ErrInvalidOriginalStorage)
	require.Nil(t, runtime)
}

func TestOriginalRuntimeTusFailureDoesNotCloseSuppliedStorage(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(root, nil, 0o600))
	deps := originalStorageDeps(t)
	supplied := &callerOwnedStorage{}
	deps.Storage = supplied
	runtime, err := host.NewOriginalRuntime(host.StorageConfig{Local: host.StorageLocalConfig{Root: root}}, deps)
	require.Error(t, err)
	require.Nil(t, runtime)
	require.Zero(t, supplied.closed)
}

type originalStorageOutbox struct{}

func (*originalStorageOutbox) Put(context.Context, string, string, time.Time) (outboxtypes.JobID, error) {
	return outboxtypes.NewJobID(), nil
}
