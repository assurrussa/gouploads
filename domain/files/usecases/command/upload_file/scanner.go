package uploadfile

import "github.com/assurrussa/gouploads/internal/filepolicy"

// NewOriginalWithContentScanner configures the optional full-content scan at
// construction time. It never mutates a running finalizer.
func NewOriginalWithContentScanner(options OriginalOptions, scanner filepolicy.Scanner) (*OriginalUseCase, error) {
	useCase, err := NewOriginal(options)
	if err != nil {
		return nil, err
	}
	useCase.core.scanner = scanner
	return useCase, nil
}
