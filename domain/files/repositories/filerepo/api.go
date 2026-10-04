package filerepo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	querybuilder "github.com/assurrussa/outbox/shared/query_builder"
	"github.com/georgysavva/scany/v2/pgxscan"

	"github.com/assurrussa/gouploads/domain/files/model"
)

const (
	tableName         = "files"
	columnID          = "id"
	columnUserID      = "user_id"
	columnObjectType  = "object_type"
	columnManagerID   = "manager_id"
	columnFilename    = "filename"
	columnObjectID    = "object_id"
	columnOriginal    = "original_filename"
	columnFolderPath  = "folder_path"
	columnIsPrimary   = "is_primary"
	columnData        = "data"
	columnSize        = "size"
	columnMimeType    = "mime_type"
	columnName        = "name"
	columnProvider    = "provider"
	columnURL         = "url"
	columnModerate    = "moderate"
	columnBlockCause  = "block_cause"
	columnFileType    = "file_type"
	columnPosition    = "position"
	columnSlug        = "slug"
	columnLocale      = "locale"
	columnDescription = "description"
	columnCreatedAt   = "created_at"
	columnUpdatedAt   = "updated_at"
	columnDeletedAt   = "deleted_at"
	columnPublishedAt = "published_at"
	MaxListLimit      = 100
)

var columns = []string{
	columnID, columnUserID, columnManagerID, columnObjectType, columnObjectID, columnOriginal,
	columnFilename, columnFolderPath, columnProvider, columnSize, columnMimeType,
	columnModerate, columnBlockCause, columnName, columnDescription, columnFileType, columnPosition, columnIsPrimary, columnURL,
	columnSlug, columnLocale, columnData, columnCreatedAt, columnUpdatedAt, columnDeletedAt, columnPublishedAt,
}

type ListFilters struct {
	Limit      int
	Offset     int
	FileType   string
	ObjectType string
	ObjectID   int64
	// SkipTotal avoids a COUNT when the caller only needs the current page.
	SkipTotal bool
}

func (r *Repo) List(ctx context.Context, filters ListFilters) ([]model.File, int, error) {
	const op = "filerepo.List"
	if filters.Limit < 0 || filters.Offset < 0 {
		return nil, 0, fmt.Errorf("%s: pagination must be nonnegative", op)
	}
	if filters.Limit == 0 {
		filters.Limit = 50
	}
	if filters.Limit > MaxListLimit {
		filters.Limit = MaxListLimit
	}
	conditions := squirrel.And{squirrel.Eq{columnDeletedAt: nil}}
	if filters.FileType != "" {
		condition, err := fileTypeCondition(filters.FileType)
		if err != nil {
			return nil, 0, err
		}
		conditions = append(conditions, condition)
	}
	if filters.ObjectType != "" {
		conditions = append(conditions, squirrel.Eq{columnObjectType: filters.ObjectType})
	}
	if filters.ObjectID > 0 {
		conditions = append(conditions, squirrel.Eq{columnObjectID: filters.ObjectID})
	}
	var total int
	if !filters.SkipTotal {
		count := querybuilder.BuilderDollar().Select("count(id)").From(tableName).Where(conditions)
		if err := r.pgsql.DB().ScanOnex(ctx, op, &total, count); err != nil {
			return nil, 0, fmt.Errorf("count files: %w", pgsql.ErrorTransform(err))
		}
	}
	query := querybuilder.BuilderDollar().Select(columns...).From(tableName).Where(conditions).
		OrderBy("created_at DESC", "id DESC").Limit(uint64(filters.Limit)).Offset(uint64(filters.Offset))
	var files []model.File
	if err := r.pgsql.DB().ScanAllx(ctx, op, &files, query); err != nil {
		return nil, 0, fmt.Errorf("query files: %w", pgsql.ErrorTransform(err))
	}
	return files, total, nil
}

func fileTypeCondition(raw string) (squirrel.Sqlizer, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	for _, kind := range []model.FileType{
		model.FileTypeUnknown,
		model.FileTypeImage,
		model.FileTypeVideo,
		model.FileTypePdf,
		model.FileTypeDocx,
		model.FileTypeLink,
		model.FileTypeText,
		model.FileTypeAudio,
	} {
		if value == kind.String() || value == kind.ToString() {
			return squirrel.Eq{columnFileType: kind}, nil
		}
	}
	// Keep the explicitly documented MIME-prefix form for repository consumers.
	switch value {
	case "image/", "video/", "audio/", "text/", "application/":
		return squirrel.Like{columnMimeType: value + "%"}, nil
	case "application/pdf", "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return squirrel.Eq{columnMimeType: value}, nil
	default:
		return nil, fmt.Errorf("unsupported file type filter %q", raw)
	}
}

func mutableFileValues(file model.File) querybuilder.Eq {
	return querybuilder.Eq{
		columnUserID: file.UserID, columnManagerID: file.ManagerID, columnObjectType: file.ObjectType, columnObjectID: file.ObjectID,
		columnOriginal: file.OriginalFileName, columnFilename: file.FileName, columnFolderPath: file.FolderPath,
		columnProvider: file.Provider, columnSize: file.Size, columnMimeType: file.MimeType, columnURL: file.URL,
		columnName: file.Name, columnDescription: file.Description, columnData: file.Data, columnIsPrimary: file.IsPrimary,
		columnFileType: file.FileType, columnPosition: file.Position, columnModerate: file.Moderate, columnBlockCause: file.BlockCause,
		columnLocale: file.Locale, columnPublishedAt: file.PublishedAt,
	}
}

func (r *Repo) Create(ctx context.Context, file model.File) (int64, error) {
	values := mutableFileValues(file)
	values[columnSlug] = file.Slug
	values[columnCreatedAt] = file.CreatedAt
	values[columnUpdatedAt] = file.UpdatedAt
	query := querybuilder.BuilderDollar().Insert(tableName).Suffix("RETURNING id").SetMap(values)
	var id int64
	if err := r.pgsql.DB().Getx(ctx, "filerepo.Create", &id, query); err != nil {
		return 0, fmt.Errorf("create file: %w", pgsql.ErrorTransform(err))
	}
	return id, nil
}

func (r *Repo) Update(ctx context.Context, id int64, file model.File) error {
	if id <= 0 {
		return errors.New("filerepo.Update: invalid id")
	}
	values := mutableFileValues(file)
	values[columnUpdatedAt] = time.Now()
	query := querybuilder.BuilderDollar().Update(tableName).SetMap(values).Where(squirrel.Eq{columnID: id, columnDeletedAt: nil})
	tag, err := r.pgsql.DB().Execx(ctx, "filerepo.Update", query)
	if err != nil {
		return fmt.Errorf("update file: %w", pgsql.ErrorTransform(err))
	}
	if tag.RowsAffected() != 1 {
		return model.ErrFileNotFound
	}
	return nil
}

func (r *Repo) GetByID(ctx context.Context, id int64) (model.File, error) {
	if id <= 0 {
		return model.File{}, errors.New("filerepo.GetByID: invalid id")
	}
	query := querybuilder.BuilderDollar().Select(columns...).From(tableName).Where(squirrel.Eq{
		columnID:        id,
		columnDeletedAt: nil,
	}).Limit(1)
	var file model.File
	if err := r.pgsql.DB().ScanOnex(ctx, "filerepo.GetByID", &file, query); err != nil {
		if pgxscan.NotFound(err) {
			return model.File{}, nil
		}
		return model.File{}, fmt.Errorf("get file: %w", pgsql.ErrorTransform(err))
	}
	return file, nil
}

func (r *Repo) DeleteByID(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("filerepo.DeleteByID: invalid id")
	}
	query := querybuilder.BuilderDollar().Update(tableName).Set(columnDeletedAt, time.Now()).Set(columnUpdatedAt, time.Now()).
		Where(squirrel.Eq{columnID: id, columnDeletedAt: nil})
	if _, err := r.pgsql.DB().Execx(ctx, "filerepo.DeleteByID", query); err != nil {
		return fmt.Errorf("delete file: %w", pgsql.ErrorTransform(err))
	}
	return nil
}

func (r *Repo) GetByObjectType(ctx context.Context, objectType string, objectID int64) ([]model.File, error) {
	query := querybuilder.BuilderDollar().Select(columns...).From(tableName).
		Where(squirrel.Eq{columnObjectType: objectType, columnObjectID: objectID, columnDeletedAt: nil}).
		OrderBy("is_primary DESC", "position ASC", "created_at DESC", "id DESC")
	var files []model.File
	if err := r.pgsql.DB().ScanAllx(ctx, "filerepo.GetByObjectType", &files, query); err != nil {
		return nil, fmt.Errorf("query object files: %w", pgsql.ErrorTransform(err))
	}
	return files, nil
}

func (r *Repo) lockPrimary(ctx context.Context, objectType string, objectID int64) error {
	if objectType == "" || objectID <= 0 {
		return errors.New("primary object binding is required")
	}
	key := fmt.Sprintf("gouploads:primary:%s:%d", objectType, objectID)
	_, err := r.pgsql.DB().Exec(ctx, "filerepo.LockPrimary", "select pg_advisory_xact_lock(hashtextextended($1, 0))", key)
	return err
}

func (r *Repo) checkPrimaryBinding(ctx context.Context, id int64, objectType string, objectID int64) error {
	file, err := r.GetByIDForUpdate(ctx, id)
	if err != nil {
		return err
	}
	if file.ID == 0 {
		return model.ErrFileNotFound
	}
	if file.ObjectID == nil || file.ObjectType.String() != objectType || file.ObjectID.Int64() != objectID {
		return model.ErrObjectBindingMismatch
	}
	return nil
}

func (r *Repo) ClearPrimary(ctx context.Context, objectType string, objectID int64, excludeID int64) error {
	return r.trxManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := r.lockPrimary(ctx, objectType, objectID); err != nil {
			return err
		}
		if excludeID > 0 {
			if err := r.checkPrimaryBinding(ctx, excludeID, objectType, objectID); err != nil {
				return err
			}
		}
		query := querybuilder.BuilderDollar().Update(tableName).Set(columnIsPrimary, false).Set(columnUpdatedAt, time.Now()).
			Where(squirrel.Eq{columnObjectType: objectType, columnObjectID: objectID, columnIsPrimary: true, columnDeletedAt: nil})
		if excludeID > 0 {
			query = query.Where(squirrel.NotEq{columnID: excludeID})
		}
		if _, err := r.pgsql.DB().Execx(ctx, "filerepo.ClearPrimary", query); err != nil {
			return fmt.Errorf("clear primary: %w", pgsql.ErrorTransform(err))
		}
		return nil
	})
}

// SetPrimary never changes the owning object. Rebinding requires a separate,
// explicitly authorized domain operation, not an implicit side effect here.
func (r *Repo) SetPrimary(ctx context.Context, id int64, objectType string, objectID int64) error {
	return r.trxManager.RunInTx(ctx, func(ctx context.Context) error {
		if err := r.lockPrimary(ctx, objectType, objectID); err != nil {
			return err
		}
		if err := r.checkPrimaryBinding(ctx, id, objectType, objectID); err != nil {
			return err
		}
		query := querybuilder.BuilderDollar().Update(tableName).Set(columnIsPrimary, true).Set(columnUpdatedAt, time.Now()).
			Where(squirrel.Eq{columnID: id, columnObjectType: objectType, columnObjectID: objectID, columnDeletedAt: nil})
		tag, err := r.pgsql.DB().Execx(ctx, "filerepo.SetPrimary", query)
		if err != nil {
			return fmt.Errorf("set primary: %w", pgsql.ErrorTransform(err))
		}
		if tag.RowsAffected() != 1 {
			return model.ErrFileNotFound
		}
		return nil
	})
}

func (r *Repo) UpdatePosition(ctx context.Context, id int64, position int) error {
	if id <= 0 {
		return errors.New("filerepo.UpdatePosition: invalid id")
	}
	query := querybuilder.BuilderDollar().Update(tableName).Set(columnPosition, position).
		Set(columnUpdatedAt, squirrel.Expr("NOW()")).Where(squirrel.Eq{columnID: id, columnDeletedAt: nil})
	tag, err := r.pgsql.DB().Execx(ctx, "filerepo.UpdatePosition", query)
	if err != nil {
		return fmt.Errorf("update file position: %w", pgsql.ErrorTransform(err))
	}
	if tag.RowsAffected() != 1 {
		return model.ErrFileNotFound
	}
	return nil
}

// CleanupExpiredFiles removes tombstones in bounded, nonblocking batches.
func (r *Repo) CleanupExpiredFiles(ctx context.Context, batchSize, minutes int) (int64, error) {
	if batchSize <= 0 {
		return 0, errors.New("CleanupExpiredFiles: invalid batchSize")
	}
	if minutes < 0 {
		minutes = 60
	}
	query := `WITH c AS (
  SELECT f.id FROM files f
  WHERE f.deleted_at < $1::timestamp - $2 * INTERVAL '1 minute'
    AND NOT EXISTS (SELECT 1 FROM file_deletions d WHERE d.file_id = f.id AND d.completed_at IS NULL)
  ORDER BY f.id ASC FOR UPDATE OF f SKIP LOCKED LIMIT $3
)
DELETE FROM files t USING c WHERE t.id = c.id;`
	tag, err := r.pgsql.DB().Exec(ctx, "CleanupExpiredFiles", query, time.Now(), minutes, batchSize)
	if err != nil {
		return 0, fmt.Errorf("clean up files: %w", pgsql.ErrorTransform(err))
	}
	return tag.RowsAffected(), nil
}
