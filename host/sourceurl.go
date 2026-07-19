package host

import (
	"context"

	filesourceurl "github.com/assurrussa/gouploads/infrastructure/storage/files/sourceurl"
)

// SourceURLResolver resolves a durable source reference immediately before a
// media processing request is dispatched.
type SourceURLResolver interface {
	Resolve(ctx context.Context, source string) (string, error)
}

// NewSourceURLResolver builds the source URL policy selected by cfg without
// expanding the Storage interface.
func NewSourceURLResolver(cfg StorageConfig) (SourceURLResolver, error) {
	return filesourceurl.New(cfg)
}
