package tusupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Retained as a test-only helper for the existing cleanup regression test.
func updateMetaUpdatedAt(filename string, updatedAt time.Time) error {
	body, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var meta storeMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return err
	}
	meta.UpdatedAt = updatedAt
	body, err = json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, body, 0o600)
}

func TestFileStoreRejectsUnconfinedIDs(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "uploads")
	neighbor := filepath.Join(parent, "neighbor")
	if err := os.MkdirAll(neighbor, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(neighbor, "keep")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../neighbor", "..", ".", "", neighbor, "../../"} {
		if err := store.Delete(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(%q): %v", id, err)
		}
		if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get(%q): %v", id, err)
		}
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "safe" {
		t.Fatalf("neighbor changed: %v", err)
	}
}

func TestFileStoreSerializesIndependentInstances(t *testing.T) {
	root := t.TempDir()
	a, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for range 10 {
		session, err := a.Create(ctx, CreateRequest{UploadLength: 8, OriginalName: "x.bin", FileName: "x.bin"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for _, store := range []*FileStore{a, b} {
			workers.Add(1)
			go func(store *FileStore) {
				defer workers.Done()
				<-start
				_, err := store.Append(ctx, session.ID, 0, []byte("data"), "")
				results <- err
			}(store)
		}
		close(start)
		workers.Wait()
		close(results)
		success, conflict := 0, 0
		for err := range results {
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrOffsetMismatch):
				conflict++
			default:
				t.Fatal(err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("success=%d conflict=%d", success, conflict)
		}
		loaded, err := a.Get(ctx, session.ID)
		if err != nil || loaded.Offset != 4 {
			t.Fatalf("offset=%d error=%v", loaded.Offset, err)
		}
	}
}

func TestFileStoreOversizeDoesNotWriteAndReadyIsImmutable(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, err := store.Create(ctx, CreateRequest{UploadLength: 4, OriginalName: "x.bin", FileName: "x.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, session.ID, 0, []byte("large"), ""); !errors.Is(err, ErrLengthExceeded) {
		t.Fatal(err)
	}
	current, err := store.Get(ctx, session.ID)
	if err != nil || current.Offset != 0 {
		t.Fatalf("rejected bytes were written: %+v %v", current, err)
	}
	if _, err := store.Append(ctx, session.ID, 0, []byte("data"), ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := store.Complete(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(result.Reader)
		_ = result.Reader.Close()
		if err != nil || !bytes.Equal(body, []byte("data")) {
			t.Fatalf("read %q: %v", body, err)
		}
	}
	if _, err := store.Append(ctx, session.ID, 4, nil, ""); !errors.Is(err, ErrUploadFinalized) {
		t.Fatalf("ready patch: %v", err)
	}
}

func TestLegacyMetadataPreservesOwnershipAndLength(t *testing.T) {
	var meta storeMeta
	body := []byte(`{
		"id":"b99634f3-bc33-42cf-9f0a-53cb08f55e80","upload_length":123,"owner_id":77,
		"original_name":"test.png","file_name":"physical.png","mime_type":"image/png","metadata":{"context":"cms"}
	}`)
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.UploadLength != 123 || meta.OwnerID != 77 || meta.OriginalName != "test.png" || meta.MimeType != "image/png" {
		t.Fatalf("legacy metadata lost: %+v", meta)
	}
}

func TestFileStoreCleanupRechecksActivityUnderLock(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := store.Create(ctx, CreateRequest{UploadLength: 4, OriginalName: "x", FileName: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := updateMetaUpdatedAt(filepath.Join(store.root, session.ID, "meta.json"), time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var removed int
	var cleanupErr error
	done := make(chan struct{})
	err = store.withSession(ctx, session.ID, func(root *os.Root) error {
		go func() { removed, cleanupErr = store.Cleanup(ctx, time.Now().Add(-time.Hour)); close(done) }()
		meta, err := readMetaAt(root, session.ID)
		if err != nil {
			return err
		}
		meta.UpdatedAt = time.Now().UTC()
		return writeMetaAt(root, session.ID, meta)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if cleanupErr != nil || removed != 0 {
		t.Fatalf("active upload removed: %d %v", removed, cleanupErr)
	}
	if _, err := store.Get(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFileStoreReadPrefixChecksOffsetAndPreservesData(t *testing.T) {
	ctx := context.Background()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("x"), SniffLen+20)
	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(len(body)), OriginalName: "x.bin", FileName: "x.bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(ctx, session.ID, 0)
	if err != nil || len(prefix) != 0 {
		t.Fatalf("empty prefix: %q %v", prefix, err)
	}
	if _, err := store.Append(ctx, session.ID, 0, body[:1], ""); err != nil {
		t.Fatal(err)
	}
	prefix, err = store.ReadPrefix(ctx, session.ID, 1)
	if err != nil || !bytes.Equal(prefix, body[:1]) {
		t.Fatalf("short prefix: %q %v", prefix, err)
	}
	if _, err := store.ReadPrefix(ctx, session.ID, 0); !errors.Is(err, ErrOffsetMismatch) {
		t.Fatalf("stale offset: %v", err)
	}
	if _, err := store.Append(ctx, session.ID, 1, body[1:], "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	prefix, err = store.ReadPrefix(ctx, session.ID, int64(len(body)))
	if err != nil || !bytes.Equal(prefix, body[:SniffLen]) {
		t.Fatalf("bounded prefix length=%d: %v", len(prefix), err)
	}
	loaded, err := store.Get(ctx, session.ID)
	if err != nil || loaded.Offset != int64(len(body)) || loaded.MimeType != "application/octet-stream" {
		t.Fatalf("prefix read or delayed MIME changed session: %+v %v", loaded, err)
	}
}
