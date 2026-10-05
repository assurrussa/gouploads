package model

import "github.com/assurrussa/gouploads/domain/files/shared"

// IsUploadCompleted recognizes finalized rows whose uploader metadata was
// deliberately cleared after durable artifacts were saved.
func (f *File) IsUploadCompleted() bool {
	data := f.GetData()
	return data.Uploader.Status == shared.FileUploadTaskStatusCompleted || (data.Uploader.Status == "" && len(data.Presets) > 0)
}
