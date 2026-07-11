package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strings"
)

var (
	finalizationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	extensionPattern       = regexp.MustCompile(`^\.[A-Za-z0-9]{1,15}$`)
)

type PromotionStorage interface {
	SavePersist(ctx context.Context, input SaveFileInput) (StoredFile, error)
	Commit(ctx context.Context, input CommitInput) (StoredFile, error)
	Exists(ctx context.Context, input ExistFileInput) (ExistFile, error)
	Open(ctx context.Context, relativePath string) (io.ReadCloser, error)
}

type PublicURLComposer func(relativePath string) string

type QuarantinePromoterConfig struct {
	DestinationDir   string
	QuarantinePrefix string
}

type PromotionResult struct {
	Checksum   string
	ObjectPath string
	PublicURL  string
	Size       int64
	MIMEType   string
	Width      int
	Height     int
}

type QuarantinePromoter struct {
	storage          PromotionStorage
	destinationDir   string
	quarantinePrefix string
	publicURL        PublicURLComposer
}

func NewQuarantinePromoter(
	storage PromotionStorage,
	config QuarantinePromoterConfig,
	publicURL PublicURLComposer,
) (*QuarantinePromoter, error) {
	if isNilPromotionStorage(storage) || publicURL == nil {
		return nil, errors.New("promotion storage and public URL composer are required")
	}
	destination, err := confinedDirectory(config.DestinationDir)
	if err != nil {
		return nil, fmt.Errorf("promotion destination: %w", err)
	}
	quarantine, err := confinedDirectory(config.QuarantinePrefix)
	if err != nil {
		return nil, fmt.Errorf("quarantine prefix: %w", err)
	}
	if destination == quarantine || strings.HasPrefix(destination, quarantine+"/") {
		return nil, errors.New("promotion destination must be outside quarantine")
	}
	return &QuarantinePromoter{
		storage: storage, destinationDir: destination,
		quarantinePrefix: quarantine, publicURL: publicURL,
	}, nil
}

func (p *QuarantinePromoter) Promote(ctx context.Context, completed TusCompleteResult) (PromotionResult, error) {
	if p == nil || isNilPromotionStorage(p.storage) || p.publicURL == nil {
		return PromotionResult{}, errors.New("quarantine promoter is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return PromotionResult{}, err
	}
	if !completed.Quarantined {
		return PromotionResult{}, errors.New("only quarantined uploads can be promoted")
	}
	if completed.Size < 0 || strings.TrimSpace(completed.MimeType) == "" || completed.Width < 0 || completed.Height < 0 {
		return PromotionResult{}, errors.New("completed upload metadata is invalid")
	}
	key := strings.TrimSpace(completed.FinalizationKey)
	if !finalizationKeyPattern.MatchString(key) {
		return PromotionResult{}, errors.New("valid finalization key is required")
	}
	fileName := key + safeExtension(completed.OriginalName)
	destination := path.Join(p.destinationDir, fileName)
	exists, err := p.storage.Exists(ctx, ExistFileInput{Path: destination})
	if err != nil {
		return PromotionResult{}, fmt.Errorf("check promoted object: %w", err)
	}
	if !exists.Exist {
		stored, storeErr := p.store(ctx, completed, fileName)
		if storeErr != nil {
			exists, err = p.storage.Exists(ctx, ExistFileInput{Path: destination})
			if err != nil || !exists.Exist {
				return PromotionResult{}, fmt.Errorf("promote quarantined object: %w", storeErr)
			}
		} else if stored.RelativePath != destination {
			return PromotionResult{}, fmt.Errorf("promoted object path %q does not match %q", stored.RelativePath, destination)
		}
	}
	checksum, size, err := p.checksum(ctx, destination)
	if err != nil {
		return PromotionResult{}, err
	}
	if size != completed.Size {
		return PromotionResult{}, fmt.Errorf("promoted object size %d does not match completed size %d", size, completed.Size)
	}
	publicURL := strings.TrimSpace(p.publicURL(destination))
	parsedURL, err := url.ParseRequestURI(publicURL)
	if err != nil || parsedURL.Host == "" || parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		return PromotionResult{}, errors.New("public URL composer must return an absolute HTTP URL")
	}
	return PromotionResult{
		Checksum: checksum, ObjectPath: destination, PublicURL: publicURL,
		Size: completed.Size, MIMEType: completed.MimeType,
		Width: completed.Width, Height: completed.Height,
	}, nil
}

func (p *QuarantinePromoter) store(
	ctx context.Context,
	completed TusCompleteResult,
	fileName string,
) (StoredFile, error) {
	if completed.Reader != nil {
		return p.storage.SavePersist(ctx, SaveFileInput{
			Dir: p.destinationDir, FileName: fileName,
			Size: completed.Size, MimeType: completed.MimeType, Reader: completed.Reader,
		})
	}
	if strings.TrimSpace(completed.RelativePath) == "" {
		return StoredFile{}, errors.New("quarantined object path or reader is required")
	}
	source, err := confinedObjectPath(completed.RelativePath)
	if err != nil || source == p.quarantinePrefix || !strings.HasPrefix(source, p.quarantinePrefix+"/") {
		return StoredFile{}, errors.New("quarantined object path is outside the configured prefix")
	}
	return p.storage.Commit(ctx, CommitInput{
		Path: source, DestDir: p.destinationDir, FileName: fileName,
	})
}

func (p *QuarantinePromoter) checksum(ctx context.Context, objectPath string) (string, int64, error) {
	reader, err := p.storage.Open(ctx, objectPath)
	if err != nil {
		return "", 0, fmt.Errorf("open promoted object: %w", err)
	}
	defer func() { _ = reader.Close() }()
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return "", 0, fmt.Errorf("checksum promoted object: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func confinedDirectory(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", errors.New("path must be a confined relative directory")
	}
	return clean, nil
}

func confinedObjectPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", errors.New("path must be a confined relative object")
	}
	return clean, nil
}

func safeExtension(name string) string {
	extension := path.Ext(strings.TrimSpace(name))
	if !extensionPattern.MatchString(extension) {
		return ""
	}
	return strings.ToLower(extension)
}

func isNilPromotionStorage(storage PromotionStorage) bool {
	if storage == nil {
		return true
	}
	value := reflect.ValueOf(storage)
	return value.Kind() == reflect.Pointer && value.IsNil()
}
