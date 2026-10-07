package externalconsumer_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/assurrussa/gouploads/host"
)

// Embed Storage to forward every unmodified operation without deep imports.
type meteredStorage struct {
	host.Storage
	staged atomic.Int64
}

func (s *meteredStorage) SaveTemp(ctx context.Context, input host.SaveFileInput) (host.StoredFile, error) {
	s.staged.Add(1)
	return s.Storage.SaveTemp(ctx, input)
}

func ExampleOriginalRuntimeDeps_storage() {
	root, err := os.MkdirTemp("", "gouploads-metered-")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	cfg := host.StorageConfig{Driver: host.StorageDriverLocal, Local: host.StorageLocalConfig{Root: root}}
	base, err := host.NewStorage(cfg)
	if err != nil {
		panic(err)
	}
	metered := &meteredStorage{Storage: base}
	deps := host.OriginalRuntimeDeps{Storage: metered}
	// Supply the host's Database, Transaction and Outbox before constructing
	// host.NewOriginalRuntime(cfg, deps). This example exercises the wrapper.
	_, err = deps.Storage.SaveTemp(context.Background(), host.SaveFileInput{
		Dir: "tmp/uploads", FileName: "sample.pdf", Size: 9, Reader: bytes.NewReader([]byte("%PDF-1.7\n")),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(metered.staged.Load())
	// Output: 1
}
