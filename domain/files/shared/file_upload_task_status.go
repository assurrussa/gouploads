package shared

type FileUploadTaskStatus string

const (
	FileUploadTaskStatusQueued     FileUploadTaskStatus = "queued"
	FileUploadTaskStatusProcessing FileUploadTaskStatus = "processing"
	FileUploadTaskStatusCompleted  FileUploadTaskStatus = "completed"
	FileUploadTaskStatusFailed     FileUploadTaskStatus = "failed"
)

func (s FileUploadTaskStatus) String() string {
	return string(s)
}

func (s FileUploadTaskStatus) IsTerminal() bool {
	switch s {
	case FileUploadTaskStatusCompleted, FileUploadTaskStatusFailed:
		return true
	default:
		return false
	}
}
