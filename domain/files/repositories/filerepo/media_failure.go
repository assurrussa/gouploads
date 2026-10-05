package filerepo

import (
	"context"
	"time"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

// MarkMediaFailed persists a terminal processing failure under the same row lock
// used by media finalization. A delayed failure cannot downgrade a completed file.
// The source is retained for diagnosis; a new logical attempt needs a new upload.
func (r *Repo) MarkMediaFailed(ctx context.Context, id int64) (model.File, bool, error) {
	var file model.File
	changed := false
	err := r.trxManager.RunInTx(ctx, func(ctx context.Context) error {
		var err error
		file, err = r.GetByIDForUpdate(ctx, id)
		if err != nil || file.ID == 0 {
			return err
		}
		data := file.GetData()
		if file.IsUploadCompleted() || data.Uploader.Status == shared.FileUploadTaskStatusFailed {
			return nil
		}
		data.Uploader.Status = shared.FileUploadTaskStatusFailed
		file.SetData(data)
		file.UpdatedAt = time.Now()
		if err := r.Update(ctx, id, file); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return file, changed, err
}
