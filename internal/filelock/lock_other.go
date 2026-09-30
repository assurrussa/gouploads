//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package filelock

import (
	"errors"
	"os"
)

func tryLock(*os.File) (bool, error) {
	return false, errors.New("local TUS process locks are unsupported on this platform")
}

func unlock(*os.File) error { return nil }
