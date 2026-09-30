// Package filelock provides cancellable advisory locks shared by processes.
// The lock file must not be unlinked while another process may acquire it.
package filelock

import (
	"context"
	"os"
	"sync"
	"time"
)

func Acquire(ctx context.Context, file *os.File) (func() error, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		locked, err := tryLock(file)
		if err != nil {
			return nil, err
		}
		if locked {
			var once sync.Once
			var unlockErr error
			return func() error {
				once.Do(func() { unlockErr = unlock(file) })
				return unlockErr
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
