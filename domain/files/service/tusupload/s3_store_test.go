//nolint:testpackage // need access to unexported helpers and types for thorough store testing
package tusupload

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	redislib "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
)

type fakeS3Client struct {
	mu              sync.Mutex
	createCalls     int
	uploadInputs    []s3.UploadPartInput
	completeInputs  []s3.CompleteMultipartUploadInput
	abortInputs     []s3.AbortMultipartUploadInput
	nextETagCounter int
}

func (f *fakeS3Client) CreateMultipartUpload(
	_ context.Context,
	_ *s3.CreateMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.CreateMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	return &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload-1")}, nil
}

func (f *fakeS3Client) AbortMultipartUpload(
	_ context.Context,
	input *s3.AbortMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.AbortMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abortInputs = append(f.abortInputs, *input)
	return &s3.AbortMultipartUploadOutput{}, nil
}

func (f *fakeS3Client) UploadPart(
	_ context.Context,
	input *s3.UploadPartInput,
	_ ...func(*s3.Options),
) (*s3.UploadPartOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadInputs = append(f.uploadInputs, *input)
	f.nextETagCounter++
	etag := fmt.Sprintf("etag-%d", f.nextETagCounter)
	return &s3.UploadPartOutput{ETag: aws.String(etag)}, nil
}

func (f *fakeS3Client) CompleteMultipartUpload(
	_ context.Context,
	input *s3.CompleteMultipartUploadInput,
	_ ...func(*s3.Options),
) (*s3.CompleteMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeInputs = append(f.completeInputs, *input)
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func TestS3Store_AppendComplete(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()

	client := &fakeS3Client{}
	domain := ceph.NewDomainHost("https://storage.example.com", "bucket", "public-read", true, false, true)
	store, err := NewS3Store(client, domain, redisClient, S3StoreConfig{
		Prefix:   "tmp/uploads",
		PartSize: ceph.MinPartSize,
	})
	require.NoError(t, err)

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: 10,
		OriginalName: "test.png",
		FileName:     "test.png",
		Metadata: map[string]string{
			"entity_type": "exercise",
			"entity_id":   "12",
		},
	})
	require.NoError(t, err)

	newOffset, err := store.Append(ctx, session.ID, 0, []byte("0123456789"), "image/png")
	require.NoError(t, err)
	require.Equal(t, int64(10), newOffset)

	complete, err := store.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, int64(10), complete.Size)
	require.True(t, strings.HasPrefix(complete.RelativePath, "tmp/uploads/"))
	require.True(t, strings.HasSuffix(complete.RelativePath, "/test.png"))
	require.NotEmpty(t, complete.URL)

	client.mu.Lock()
	require.Equal(t, 1, client.createCalls)
	require.Len(t, client.uploadInputs, 1)
	require.Len(t, client.completeInputs, 1)
	client.mu.Unlock()
}

func TestS3Store_AppendChunkTooSmall(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()

	client := &fakeS3Client{}
	domain := ceph.NewDomainHost("https://storage.example.com", "bucket", "public-read", true, false, true)
	store, err := NewS3Store(client, domain, redisClient, S3StoreConfig{
		Prefix:   "tmp/uploads",
		PartSize: ceph.MinPartSize,
	})
	require.NoError(t, err)

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: int64(ceph.MinPartSize) + 1,
		OriginalName: "test.png",
		FileName:     "test.png",
	})
	require.NoError(t, err)

	_, err = store.Append(ctx, session.ID, 0, []byte("small"), "image/png")
	require.ErrorIs(t, err, ErrChunkTooSmall)
}

func TestS3Store_Cleanup(t *testing.T) {
	ctx := context.Background()
	redisClient := newFakeRedis()

	client := &fakeS3Client{}
	domain := ceph.NewDomainHost("https://storage.example.com", "bucket", "public-read", true, false, true)
	store, err := NewS3Store(client, domain, redisClient, S3StoreConfig{
		Prefix:   "tmp/uploads",
		PartSize: ceph.MinPartSize,
	})
	require.NoError(t, err)

	session, err := store.Create(ctx, CreateRequest{
		UploadLength: 5,
		OriginalName: "test.png",
		FileName:     "test.png",
	})
	require.NoError(t, err)

	_, err = store.Append(ctx, session.ID, 0, []byte("12345"), "image/png")
	require.NoError(t, err)

	loaded, err := store.loadSession(ctx, session.ID)
	require.NoError(t, err)
	loaded.UpdatedAt = time.Now().Add(-2 * time.Hour)
	require.NoError(t, store.saveSession(ctx, loaded))

	removed, err := store.Cleanup(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	_, err = store.Get(ctx, session.ID)
	require.ErrorIs(t, err, ErrNotFound)

	client.mu.Lock()
	require.Len(t, client.abortInputs, 1)
	client.mu.Unlock()
}

type fakeRedis struct {
	mu    sync.Mutex
	kv    map[string]redisEntry
	zsets map[string]map[string]float64
}

type redisEntry struct {
	value     string
	expiresAt time.Time
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		kv:    make(map[string]redisEntry),
		zsets: make(map[string]map[string]float64),
	}
}

func (f *fakeRedis) Get(ctx context.Context, key string) *redislib.StringCmd {
	cmd := redislib.NewStringCmd(ctx)
	f.mu.Lock()
	entry, ok := f.kv[key]
	if ok && !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(f.kv, key)
		ok = false
	}
	f.mu.Unlock()
	if !ok {
		cmd.SetErr(redislib.Nil)
		return cmd
	}
	cmd.SetVal(entry.value)
	return cmd
}

func (f *fakeRedis) Set(ctx context.Context, key string, value any, expiration time.Duration) *redislib.StatusCmd {
	cmd := redislib.NewStatusCmd(ctx)
	var str string
	switch v := value.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		str = fmt.Sprint(v)
	}

	var expiresAt time.Time
	if expiration > 0 {
		expiresAt = time.Now().Add(expiration)
	}

	f.mu.Lock()
	f.kv[key] = redisEntry{value: str, expiresAt: expiresAt}
	f.mu.Unlock()
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedis) Del(ctx context.Context, keys ...string) *redislib.IntCmd {
	cmd := redislib.NewIntCmd(ctx)
	var removed int64
	f.mu.Lock()
	for _, key := range keys {
		if _, ok := f.kv[key]; ok {
			delete(f.kv, key)
			removed++
		}
	}
	f.mu.Unlock()
	cmd.SetVal(removed)
	return cmd
}

func (f *fakeRedis) ZAdd(ctx context.Context, key string, members ...redislib.Z) *redislib.IntCmd {
	cmd := redislib.NewIntCmd(ctx)
	f.mu.Lock()
	set := f.zsets[key]
	if set == nil {
		set = make(map[string]float64)
		f.zsets[key] = set
	}
	var added int64
	for _, member := range members {
		memberKey := fmt.Sprint(member.Member)
		if _, ok := set[memberKey]; !ok {
			added++
		}
		set[memberKey] = member.Score
	}
	f.mu.Unlock()
	cmd.SetVal(added)
	return cmd
}

func (f *fakeRedis) ZRangeByScore(ctx context.Context, key string, opt *redislib.ZRangeBy) *redislib.StringSliceCmd {
	cmd := redislib.NewStringSliceCmd(ctx)
	minScore, minInf := parseScore(opt.Min)
	maxScore, maxInf := parseScore(opt.Max)

	f.mu.Lock()
	set := f.zsets[key]
	f.mu.Unlock()

	if len(set) == 0 {
		cmd.SetVal([]string{})
		return cmd
	}

	type item struct {
		member string
		score  float64
	}
	items := make([]item, 0, len(set))
	for member, score := range set {
		items = append(items, item{member: member, score: score})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			return items[i].member < items[j].member
		}
		return items[i].score < items[j].score
	})

	filtered := make([]string, 0, len(items))
	for _, it := range items {
		if !minInf && it.score < minScore {
			continue
		}
		if !maxInf && it.score > maxScore {
			continue
		}
		filtered = append(filtered, it.member)
	}

	offset := int(opt.Offset)
	if offset > 0 && offset < len(filtered) {
		filtered = filtered[offset:]
	} else if offset >= len(filtered) {
		filtered = nil
	}
	if opt.Count >= 0 && int(opt.Count) < len(filtered) {
		filtered = filtered[:opt.Count]
	}

	cmd.SetVal(filtered)
	return cmd
}

func (f *fakeRedis) ZRem(ctx context.Context, key string, members ...any) *redislib.IntCmd {
	cmd := redislib.NewIntCmd(ctx)
	f.mu.Lock()
	set := f.zsets[key]
	var removed int64
	for _, member := range members {
		memberKey := fmt.Sprint(member)
		if _, ok := set[memberKey]; ok {
			delete(set, memberKey)
			removed++
		}
	}
	if len(set) == 0 {
		delete(f.zsets, key)
	}
	f.mu.Unlock()
	cmd.SetVal(removed)
	return cmd
}

func parseScore(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "-inf":
		return 0, true
	case "+inf", "inf":
		return 0, true
	default:
		value, _ := strconv.ParseFloat(strings.TrimPrefix(raw, "("), 64)
		return value, false
	}
}

var _ s3Client = (*fakeS3Client)(nil)
