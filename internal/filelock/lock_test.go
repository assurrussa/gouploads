//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows

package filelock_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/assurrussa/gouploads/internal/filelock"
)

func openTestLock(t *testing.T, path string) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestLockCancellationAndReacquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first := openTestLock(t, path)
	second := openTestLock(t, path)
	release, err := filelock.Acquire(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := filelock.Acquire(ctx, second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	releaseSecond, err := filelock.Acquire(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if err := releaseSecond(); err != nil {
		t.Fatal(err)
	}
}

func TestLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("GOUPLOADS_LOCK_PROBE"); path != "" {
		probeProcessLock(t, path)
		return
	}
	path := filepath.Join(t.TempDir(), "lock")
	release, err := filelock.Acquire(context.Background(), openTestLock(t, path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	probe := func(expected string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		//nolint:gosec // Execute only this test binary with a fixed helper test selector.
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockAcrossProcesses$")
		cmd.Env = append(os.Environ(), "GOUPLOADS_LOCK_PROBE="+path, "GOUPLOADS_LOCK_EXPECTED="+expected)
		return cmd.Run()
	}
	if err := probe("busy"); err != nil {
		t.Fatalf("process lock probe failed: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := probe("available"); err != nil {
		t.Fatal(err)
	}
}

func probeProcessLock(t *testing.T, path string) {
	t.Helper()
	//nolint:gosec // Path is supplied by this test's parent process from t.TempDir.
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	release, err := filelock.Acquire(ctx, file)
	if os.Getenv("GOUPLOADS_LOCK_EXPECTED") == "busy" {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected a blocked process lock, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
