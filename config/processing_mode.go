package config

import (
	"errors"
	"fmt"
)

// ProcessingMode selects how NEW uploads are finalized. Persisted jobs keep
// their original job type when an operator changes this setting.
type ProcessingMode string

const (
	ProcessingOriginalOnly ProcessingMode = "original_only"
	ProcessingMediaResizer ProcessingMode = "media_resizer"
)

var ErrProcessingDisabled = errors.New("media processing is disabled")

// Resolve returns the default for an empty value and rejects misspellings.
func (m ProcessingMode) Resolve() (ProcessingMode, error) {
	switch m {
	case "", ProcessingOriginalOnly:
		return ProcessingOriginalOnly, nil
	case ProcessingMediaResizer:
		return ProcessingMediaResizer, nil
	default:
		return "", fmt.Errorf("unknown upload processing mode %q", m)
	}
}
