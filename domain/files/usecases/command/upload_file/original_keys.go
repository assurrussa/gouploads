package uploadfile

import (
	"errors"
	"path"
	"strings"
)

// validateOriginalKey accepts object-store keys, not URLs or filesystem paths.
// Prefixes are operator-owned; request/callback input cannot set them.
func validateOriginalKey(key string, prefixes []string) error {
	if key == "" || strings.ContainsAny(key, "\\\x00:") || strings.HasPrefix(key, "/") || path.Clean(key) != key {
		return errors.New("original source must be a canonical relative storage key")
	}
	if key == "." || key == ".." || strings.HasPrefix(key, "../") {
		return errors.New("original source escapes storage")
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix+"/") {
			return nil
		}
	}
	return errors.New("original source is outside configured staging prefixes")
}

func normalizeOriginalPrefixes(prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		return []string{"tmp/uploads", "staging/v1/tus"}, nil
	}
	result := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || prefix == "." || prefix == ".." || strings.HasPrefix(prefix, "../") ||
			strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix, "\\\x00:") || path.Clean(prefix) != prefix {
			return nil, errors.New("original staging prefix must be a canonical relative directory")
		}
		result = append(result, prefix)
	}
	return result, nil
}
