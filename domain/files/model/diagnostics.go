package model

import "github.com/assurrussa/gouploads/domain/files/shared"

// FileLifecycleSnapshot contains only lifecycle evidence, never artifact paths,
// finalization keys, bindings, callback metadata or deletion payloads.
// FinalizationRecorded means application handoff was recorded, not that storage
// processing completed. Missing records do not prove artifacts are absent.
type FileLifecycleSnapshot struct {
	FileExists           bool
	FileDeleted          bool
	UploadStatus         shared.FileUploadTaskStatus
	HasPresets           bool
	FinalizationRecorded bool
	DeletionPlanned      bool
	DeletionCompleted    bool
}
