package filesanitize

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	segmentPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]*$`)
	fileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// EnsureRelativePath normalizes a path making sure it stays within the storage root.
func EnsureRelativePath(p string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	value = strings.TrimLeft(value, "/")
	if value == "" {
		return "", errors.New("empty path")
	}

	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid path segment %q", segment)
		}
	}

	return value, nil
}

// EnsureRelativeDir normalizes a path making sure it stays within the storage root.
func EnsureRelativeDir(p string) string {
	folder := filepath.Dir(strings.TrimSpace(strings.ReplaceAll(p, "\\", "/")))
	if folder == "." || folder == ".." || folder == "" {
		return ""
	}

	if folder[0] == '/' {
		folder = folder[1:]
	}

	return folder
}

// BuildSafePath joins sanitized segments into a safe relative path.
func BuildSafePath(segments ...string) (string, error) {
	if len(segments) == 0 {
		return "", errors.New("no segments provided")
	}

	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		cleanSegment, err := SanitizeSegment(segment)
		if err != nil {
			return "", err
		}
		clean = append(clean, cleanSegment)
	}

	if len(clean) == 0 {
		return "", errors.New("no valid segments")
	}

	return strings.Join(clean, "/"), nil
}

// SanitizeSegments normalizes each segment and returns the sanitized list.
func SanitizeSegments(segments []string) ([]string, error) {
	if len(segments) == 0 {
		return nil, errors.New("no segments provided")
	}

	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		cleanSegment, err := SanitizeSegment(segment)
		if err != nil {
			return nil, err
		}
		clean = append(clean, cleanSegment)
	}

	if len(clean) == 0 {
		return nil, errors.New("no valid segments")
	}

	return clean, nil
}

// SanitizeSegment lowercases and validates a single path segment.
func SanitizeSegment(segment string) (string, error) {
	seg := strings.ToLower(strings.TrimSpace(segment))
	seg = strings.ReplaceAll(seg, " ", "-")
	for strings.Contains(seg, "--") {
		seg = strings.ReplaceAll(seg, "--", "-")
	}
	seg = strings.Trim(seg, "-")
	if seg == "" {
		return "", errors.New("empty segment")
	}
	if !segmentPattern.MatchString(seg) {
		return "", fmt.Errorf("segment %q contains forbidden characters", segment)
	}
	return seg, nil
}

// SanitizeFileName normalizes a file name and enforces the allowed character set.
func SanitizeFileName(name string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "" {
		return "", errors.New("empty file name")
	}
	if strings.ContainsAny(clean, "/\\") {
		return "", errors.New("file name contains path separators")
	}
	if strings.Contains(clean, "..") {
		return "", errors.New("file name contains invalid sequence")
	}
	if !fileNamePattern.MatchString(clean) {
		return "", fmt.Errorf("file name %q contains forbidden characters", name)
	}
	return clean, nil
}
