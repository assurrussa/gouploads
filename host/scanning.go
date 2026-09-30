package host

import (
	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/internal/filepolicy"
)

// ContentScanner reads the complete, size-bounded staged artifact before it is
// published. A nil scanner means no malware/content approval has taken place.
type (
	ContentScanner      = filepolicy.Scanner
	ContentScannerFunc  = filepolicy.ScannerFunc
	ContentScanMetadata = filepolicy.Metadata
)

// BatchError reports the first failed index. UploadBatch returns the accepted
// prefix alongside this error; callers must not blindly retry that prefix.
type BatchError = uploadservice.BatchError

var (
	ErrFinalizationConflict  = model.ErrFinalizationConflict
	ErrFinalizationGone      = model.ErrFinalizationGone
	ErrObjectBindingMismatch = model.ErrObjectBindingMismatch
)
