// Package filepolicy contains the limits and content rules shared by ingestion
// and finalization. It does not treat MIME sniffing as malware scanning.
package filepolicy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // Register GIF header decoding.
	_ "image/jpeg" // Register JPEG header decoding.
	_ "image/png"  // Register PNG header decoding.
	"io"
	"mime"
	"strings"
)

const (
	mimeJPEG     = "image/jpeg"
	MaxFileSize  = int64(10 << 30)
	HeaderBudget = int64(1 << 20)
)

var ErrSizeMismatch = errors.New("file size does not match the declared size or limit")

// Metadata describes a private staged object presented to a full-content scanner.
type Metadata struct {
	OriginalName string
	ContentType  string
	Size         int64
}

// Scanner inspects the complete private source before publication. Implementations
// must honor ctx, reject unreadable/truncated input, and must not retain reader.
// A nil scanner disables scanning; it does not constitute a safety approval.
type Scanner interface {
	Scan(ctx context.Context, reader io.Reader, metadata Metadata) error
}

type ScannerFunc func(context.Context, io.Reader, Metadata) error

func (fn ScannerFunc) Scan(ctx context.Context, reader io.Reader, metadata Metadata) error {
	return fn(ctx, reader, metadata)
}

func NormalizeMIME(raw string) string {
	value, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(value)
}

// Extension is deliberately independent of the host OS MIME database: retries on
// different workers must select the same immutable artifact key.
func Extension(contentType string) string {
	switch NormalizeMIME(contentType) {
	case mimeJPEG:
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "application/pdf":
		return ".pdf"
	default:
		return ""
	}
}

func Allowed(contentType string) bool { return Extension(contentType) != "" }

// ValidateOriginalConfig rejects a policy that can accept originals which the
// finalizer cannot store. Call after merging defaults, before accepting bytes.
func ValidateOriginalConfig(limit int64, extensions []string, allowed map[string][]string) error {
	if limit <= 0 || limit > MaxFileSize {
		return fmt.Errorf("original upload size limit must be between 1 and %d", MaxFileSize)
	}
	if len(extensions) == 0 || len(allowed) == 0 {
		return errors.New("original uploads require an explicit extension and MIME allowlist")
	}
	for _, raw := range extensions {
		ext := strings.ToLower(strings.TrimSpace(raw))
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		mimes := allowed[ext]
		if len(mimes) == 0 {
			return fmt.Errorf("extension %q has no allowed MIME types", ext)
		}
		for _, rawMIME := range mimes {
			contentType := NormalizeMIME(rawMIME)
			want := Extension(contentType)
			if want == "" || (ext != want && (contentType != mimeJPEG || ext != ".jpeg")) {
				return fmt.Errorf("original upload policy does not support %q with %q", ext, rawMIME)
			}
		}
	}
	return nil
}

// InspectDimensions bounds header parsing and preserves every consumed byte.
// A large/unsupported header results in unknown dimensions, not unbounded memory.
// The caller must install its whole-file size limit before this function.
func InspectDimensions(reader io.Reader, contentType string) (replayed io.Reader, width, height int) {
	if !strings.HasPrefix(NormalizeMIME(contentType), "image/") {
		return reader, 0, 0
	}
	var consumed bytes.Buffer
	cfg, _, err := image.DecodeConfig(io.TeeReader(io.LimitReader(reader, HeaderBudget), &consumed))
	replay := io.MultiReader(bytes.NewReader(consumed.Bytes()), reader)
	if err != nil {
		return replay, 0, 0
	}
	return replay, cfg.Width, cfg.Height
}

// ExactReader reports size errors as a distinct error (not io.ErrUnexpectedEOF,
// which multipart uploaders may legitimately interpret as a short final part).
// This prevents a storage adapter from committing oversized/truncated content.
func ExactReader(ctx context.Context, reader io.Reader, expected, limit int64) io.Reader {
	return &exactReader{ctx: ctx, reader: reader, expected: expected, limit: limit}
}

type exactReader struct {
	ctx      context.Context //nolint:containedctx // io.Reader needs the request context to check cancellation on every Read.
	reader   io.Reader
	expected int64
	limit    int64
	n        int64
	done     bool
}

func (r *exactReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.done {
		return 0, io.EOF
	}
	bound := r.limit
	if r.expected > 0 && (bound <= 0 || r.expected < bound) {
		bound = r.expected
	}
	if bound <= 0 {
		return 0, ErrSizeMismatch
	}
	remaining := bound - r.n
	if remaining < 0 {
		return 0, ErrSizeMismatch
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := r.reader.Read(p)
	r.n += int64(n)
	if r.n > bound {
		return n, ErrSizeMismatch
	}
	if errors.Is(err, io.EOF) {
		if r.expected > 0 && r.n != r.expected {
			return n, ErrSizeMismatch
		}
		r.done = true
	}
	return n, err
}
