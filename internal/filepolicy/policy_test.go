package filepolicy_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/assurrussa/gouploads/internal/filepolicy"
)

func TestExactReader(t *testing.T) {
	for _, tc := range []struct {
		name            string
		data            string
		expected, limit int64
		valid           bool
	}{
		{"exact", "1234", 4, 4, true},
		{"short", "123", 4, 4, false},
		{"long", "12345", 4, 10, false},
		{"limit", "12345", 0, 4, false},
		{"unknown", "1234", 0, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := io.ReadAll(filepolicy.ExactReader(context.Background(), bytes.NewBufferString(tc.data), tc.expected, tc.limit))
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, filepolicy.ErrSizeMismatch) {
				t.Fatalf("got %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := io.ReadAll(filepolicy.ExactReader(ctx, bytes.NewBufferString("x"), 1, 1))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDimensionsBoundedAndReplay(t *testing.T) {
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), body.Bytes()...)
	replay, w, h := filepolicy.InspectDimensions(bytes.NewReader(original), "image/png")
	if w != 2 || h != 3 {
		t.Fatalf("dimensions %dx%d", w, h)
	}
	got, err := io.ReadAll(replay)
	if err != nil || !bytes.Equal(original, got) {
		t.Fatalf("replay failed: %v", err)
	}

	// JPEG APP markers can precede the frame indefinitely. Parsing must not
	// consume the entire upload or allocate an upload-sized replay buffer.
	segment := append([]byte{0xff, 0xe0, 0xff, 0xff}, make([]byte, 65533)...)
	malformed := make([]byte, 2, 2+len(segment)*32)
	copy(malformed, []byte{0xff, 0xd8})
	for range 32 {
		malformed = append(malformed, segment...)
	}
	counter := &countReader{reader: bytes.NewReader(malformed)}
	replay, _, _ = filepolicy.InspectDimensions(counter, "image/jpeg")
	if counter.n > filepolicy.HeaderBudget {
		t.Fatalf("read %d before limit", counter.n)
	}
	got, err = io.ReadAll(replay)
	if err != nil || !bytes.Equal(malformed, got) {
		t.Fatalf("bounded replay failed: %v", err)
	}
}

type countReader struct {
	reader io.Reader
	n      int64
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.n += int64(n)
	return n, err
}

func TestOriginalPolicyAndDeterministicExtension(t *testing.T) {
	if filepolicy.Extension("image/jpeg") != ".jpg" {
		t.Fatal("unstable JPEG extension")
	}
	if err := filepolicy.ValidateOriginalConfig(10<<20, []string{".jpg"}, map[string][]string{".jpg": {"image/jpeg"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ ext, mime string }{{".svg", "image/svg+xml"}, {".txt", "text/plain"}, {".jpg", "image/png"}} {
		if err := filepolicy.ValidateOriginalConfig(10<<20, []string{tc.ext}, map[string][]string{tc.ext: {tc.mime}}); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
