package host_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestQuarantinePromoterIsIdempotentForRemoteObject(t *testing.T) {
	t.Parallel()
	storage := newPromotionStorage(map[string][]byte{"quarantine/uploads/session": []byte("cms media")})
	promoter, err := host.NewQuarantinePromoter(storage, host.QuarantinePromoterConfig{
		DestinationDir: "uploads/cms/originals", QuarantinePrefix: "quarantine/uploads",
	}, func(objectPath string) string { return "https://media.example.test/" + objectPath })
	require.NoError(t, err)
	completed := host.TusCompleteResult{
		RelativePath: "quarantine/uploads/session", OriginalName: "Hero.JPG",
		FinalizationKey: "stable-key", Quarantined: true, Size: 9, MimeType: "image/jpeg",
	}
	first, err := promoter.Promote(context.Background(), completed)
	require.NoError(t, err)
	second, err := promoter.Promote(context.Background(), completed)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, "uploads/cms/originals/stable-key.jpg", first.ObjectPath)
	require.Equal(t, "20cb8a59249d6608d8b4381f1e586d47708387b35c78bc6e4094195b65331391", first.Checksum)
	require.Equal(t, "https://media.example.test/uploads/cms/originals/stable-key.jpg", first.PublicURL)
	require.Equal(t, int64(9), first.Size)
	require.Equal(t, "image/jpeg", first.MIMEType)
	require.False(t, storage.has("quarantine/uploads/session"))
}

func TestQuarantinePromoterStoresLocalReader(t *testing.T) {
	t.Parallel()
	storage := newPromotionStorage(nil)
	promoter, err := host.NewQuarantinePromoter(storage, host.QuarantinePromoterConfig{
		DestinationDir: "uploads/cms", QuarantinePrefix: "tmp/tus",
	}, func(objectPath string) string { return "https://media.example.test/" + objectPath })
	require.NoError(t, err)
	result, err := promoter.Promote(context.Background(), host.TusCompleteResult{
		Reader: io.NopCloser(bytes.NewBufferString("local")), OriginalName: "note.txt",
		FinalizationKey: "local-key", Quarantined: true, Size: 5, MimeType: "text/plain",
	})
	require.NoError(t, err)
	require.Equal(t, "uploads/cms/local-key.txt", result.ObjectPath)
	require.True(t, storage.has(result.ObjectPath))
}

func TestQuarantinePromoterRecoversCommittedResultAfterAmbiguousError(t *testing.T) {
	t.Parallel()
	storage := newPromotionStorage(map[string][]byte{"quarantine/session": []byte("payload")})
	storage.failAfterCommit = true
	promoter, err := host.NewQuarantinePromoter(storage, host.QuarantinePromoterConfig{
		DestinationDir: "uploads/cms", QuarantinePrefix: "quarantine",
	}, func(objectPath string) string { return "https://cdn.example.test/" + objectPath })
	require.NoError(t, err)
	result, err := promoter.Promote(context.Background(), host.TusCompleteResult{
		RelativePath: "quarantine/session", OriginalName: "asset.bin",
		FinalizationKey: "ambiguous", Quarantined: true, Size: 7, MimeType: "application/octet-stream",
	})
	require.NoError(t, err)
	require.Equal(t, "uploads/cms/ambiguous.bin", result.ObjectPath)
}

func TestQuarantinePromoterRejectsUnsafeOrIncompleteInput(t *testing.T) {
	t.Parallel()
	storage := newPromotionStorage(nil)
	_, err := host.NewQuarantinePromoter(storage, host.QuarantinePromoterConfig{
		DestinationDir: "quarantine/public", QuarantinePrefix: "quarantine",
	}, func(string) string { return "https://cdn.example.test/value" })
	require.ErrorContains(t, err, "outside quarantine")
	promoter, err := host.NewQuarantinePromoter(storage, host.QuarantinePromoterConfig{
		DestinationDir: "uploads/cms", QuarantinePrefix: "quarantine",
	}, func(string) string { return "https://cdn.example.test/value" })
	require.NoError(t, err)
	_, err = promoter.Promote(context.Background(), host.TusCompleteResult{FinalizationKey: "key"})
	require.ErrorContains(t, err, "only quarantined")
	_, err = promoter.Promote(context.Background(), host.TusCompleteResult{
		FinalizationKey: "../key", Quarantined: true, RelativePath: "quarantine/session",
		Size: 7, MimeType: "application/octet-stream",
	})
	require.ErrorContains(t, err, "finalization key")
}

type promotionStorage struct {
	mu              sync.Mutex
	objects         map[string][]byte
	failAfterCommit bool
}

func newPromotionStorage(objects map[string][]byte) *promotionStorage {
	result := &promotionStorage{objects: make(map[string][]byte)}
	for key, value := range objects {
		result.objects[key] = append([]byte(nil), value...)
	}
	return result
}

func (s *promotionStorage) SavePersist(_ context.Context, input host.SaveFileInput) (host.StoredFile, error) {
	data, err := io.ReadAll(input.Reader)
	if err != nil {
		return host.StoredFile{}, err
	}
	objectPath := path.Join(input.Dir, input.FileName)
	s.mu.Lock()
	s.objects[objectPath] = data
	s.mu.Unlock()
	return host.StoredFile{RelativePath: objectPath, Size: int64(len(data)), MimeType: input.MimeType}, nil
}

func (s *promotionStorage) Commit(_ context.Context, input host.CommitInput) (host.StoredFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, exists := s.objects[input.Path]
	if !exists {
		return host.StoredFile{}, errors.New("source is missing")
	}
	objectPath := path.Join(input.DestDir, input.FileName)
	s.objects[objectPath] = append([]byte(nil), data...)
	delete(s.objects, input.Path)
	if s.failAfterCommit {
		return host.StoredFile{}, errors.New("ambiguous commit response")
	}
	return host.StoredFile{RelativePath: objectPath, Size: int64(len(data))}, nil
}

func (s *promotionStorage) Exists(_ context.Context, input host.ExistFileInput) (host.ExistFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.objects[input.Path]
	return host.ExistFile{Exist: exists}, nil
}

func (s *promotionStorage) Open(_ context.Context, objectPath string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, exists := s.objects[objectPath]
	if !exists {
		return nil, errors.New("object is missing")
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), data...))), nil
}

func (s *promotionStorage) has(objectPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.objects[objectPath]
	return exists
}

var _ host.PromotionStorage = (*promotionStorage)(nil)
