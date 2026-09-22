package filerepo

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	querybuilder "github.com/assurrussa/outbox/shared/query_builder"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/georgysavva/scany/v2/pgxscan"

	"github.com/assurrussa/gouploads/domain/files/model"
)

const (
	tableName = "files"

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
)

var columns = []string{
	columnID, columnUserID, columnManagerID, columnObjectType, columnObjectID, columnOriginal,
	columnFilename, columnFolderPath, columnProvider, columnSize, columnMimeType,
	columnModerate, columnBlockCause, columnName, columnDescription, columnFileType, columnPosition, columnIsPrimary, columnURL,
	columnSlug, columnLocale, columnData, columnCreatedAt, columnUpdatedAt, columnDeletedAt, columnPublishedAt,
}

// ListFilters фильтры для списка файлов.
type ListFilters struct {
	Limit      int
	Offset     int
	FileType   string
	ObjectType string
	ObjectID   int64
}

// List возвращает список файлов с фильтрами.
func (r *Repo) List(ctx context.Context, filters ListFilters) ([]model.File, int, error) {
	const op = "filerepo.List"

	// Базовый запрос для подсчета
	countBuilder := querybuilder.BuilderDollar().
		Select(fmt.Sprintf("count(%s) as total", columnID)).
		From(tableName).
		Where(squirrel.Eq{columnDeletedAt: nil})

	// Базовый запрос для получения данных
	listBuilder := querybuilder.BuilderDollar().
		Select(columns...).
		From(tableName).
		Where(squirrel.Eq{columnDeletedAt: nil})

	// Применяем фильтры
	if filters.FileType != "" {
		countBuilder = countBuilder.Where(squirrel.Like{columnMimeType: filters.FileType + "%"})
		listBuilder = listBuilder.Where(squirrel.Like{columnMimeType: filters.FileType + "%"})
	}

	if filters.ObjectType != "" {
		countBuilder = countBuilder.Where(squirrel.Eq{columnObjectType: filters.ObjectType})
		listBuilder = listBuilder.Where(squirrel.Eq{columnObjectType: filters.ObjectType})
	}

	if filters.ObjectID > 0 {
		countBuilder = countBuilder.Where(squirrel.Eq{columnObjectID: filters.ObjectID})
		listBuilder = listBuilder.Where(squirrel.Eq{columnObjectID: filters.ObjectID})
	}

	// Получаем общее количество
	var total int
	if err := r.pgsql.DB().ScanOnex(ctx, op, &total, countBuilder); err != nil {
		return nil, 0, fmt.Errorf("failed to count files: %w", pgsql.ErrorTransform(err))
	}

	// Применяем сортировку и пагинацию
	listBuilder = listBuilder.
		OrderBy("created_at DESC").
		Limit(uint64(filters.Limit)).
		Offset(uint64(filters.Offset))

	var files []model.File
	if err := r.pgsql.DB().ScanAllx(ctx, op, &files, listBuilder); err != nil {
		return nil, 0, fmt.Errorf("failed to query files: %w", pgsql.ErrorTransform(err))
	}

	return files, total, nil
}

// Create создает новый файл в базе данных.
func (r *Repo) Create(ctx context.Context, file model.File) (int64, error) {
	const op = "filerepo.Create"

	builder := querybuilder.BuilderDollar().
		Insert(tableName).
		Suffix("RETURNING id").
		SetMap(querybuilder.Eq{
			columnUserID:      file.UserID,
			columnManagerID:   file.ManagerID,
			columnObjectType:  file.ObjectType,
			columnObjectID:    file.ObjectID,
			columnOriginal:    file.OriginalFileName,
			columnFilename:    file.FileName,
			columnFolderPath:  file.FolderPath,
			columnProvider:    file.Provider,
			columnSize:        file.Size,
			columnMimeType:    file.MimeType,
			columnURL:         file.URL,
			columnSlug:        file.Slug,
			columnName:        file.Name,
			columnDescription: file.Description,
			columnData:        file.Data,
			columnIsPrimary:   file.IsPrimary,
			columnCreatedAt:   file.CreatedAt,
			columnUpdatedAt:   file.UpdatedAt,
		})

	var id int64
	if err := r.pgsql.DB().Getx(ctx, op, &id, builder); err != nil {
		return 0, fmt.Errorf("failed to create file: %w", pgsql.ErrorTransform(err))
	}

	return id, nil
}

func (r *Repo) Update(ctx context.Context, id int64, file model.File) error {
	const op = "filerepo.Update"

	builder := querybuilder.BuilderDollar().
		Update(tableName).
		SetMap(querybuilder.Eq{
			columnUserID:      file.UserID,
			columnManagerID:   file.ManagerID,
			columnObjectType:  file.ObjectType,
			columnObjectID:    file.ObjectID,
			columnOriginal:    file.OriginalFileName,
			columnFilename:    file.FileName,
			columnFolderPath:  file.FolderPath,
			columnProvider:    file.Provider,
			columnSize:        file.Size,
			columnMimeType:    file.MimeType,
			columnURL:         file.URL,
			columnName:        file.Name,
			columnData:        file.Data,
			columnDescription: file.Description,
			columnIsPrimary:   file.IsPrimary,
			columnCreatedAt:   file.CreatedAt,
			columnUpdatedAt:   file.UpdatedAt,
		}).Where(squirrel.Eq{columnID: id})

	if _, err := r.pgsql.DB().Execx(ctx, op, builder); err != nil {
		return fmt.Errorf("error update: %w", pgsql.ErrorTransform(err))
	}

	return nil
}

// GetByID получает файл по ID.
func (r *Repo) GetByID(ctx context.Context, id int64) (model.File, error) {
	const op = "filerepo.GetByID"

	if id <= 0 {
		return model.File{}, fmt.Errorf("%s: invalid id", op)
	}

	builder := querybuilder.BuilderDollar().
		Select(columns...).
		From(tableName).
		Where(squirrel.Eq{columnID: id, columnDeletedAt: nil}).
		Limit(1)

	var file model.File
	if err := r.pgsql.DB().ScanOnex(ctx, op, &file, builder); err != nil {
		if pgxscan.NotFound(err) {
			return model.File{}, nil
		}

		return model.File{}, fmt.Errorf("%s: error get: %w", op, pgsql.ErrorTransform(err))
	}

	return file, nil
}

// DeleteByID удаляет файл по ID (soft delete).
func (r *Repo) DeleteByID(ctx context.Context, id int64) error {
	const op = "filerepo.DeleteByID"

	if id <= 0 {
		return fmt.Errorf("%s: invalid id", op)
	}

	builder := querybuilder.BuilderDollar().
		Update(tableName).
		Set(columnDeletedAt, time.Now()).
		Where(squirrel.Eq{columnID: id}).
		Where(squirrel.Eq{columnDeletedAt: nil})

	if _, err := r.pgsql.DB().Execx(ctx, op, builder); err != nil {
		return fmt.Errorf("failed to delete file: %w", pgsql.ErrorTransform(err))
	}

	return nil
}

// GetByObjectType возвращает файлы по типу объекта.
func (r *Repo) GetByObjectType(ctx context.Context, objectType string, objectID int64) ([]model.File, error) {
	const op = "filerepo.GetByObjectType"

	builder := querybuilder.BuilderDollar().
		Select(columns...).
		From(tableName).
		Where(squirrel.Eq{columnObjectType: objectType}).
		Where(squirrel.Eq{columnObjectID: objectID}).
		Where(squirrel.Eq{columnDeletedAt: nil}).
		OrderBy("is_primary DESC", "position ASC", "created_at DESC")

	var files []model.File
	if err := r.pgsql.DB().ScanAllx(ctx, op, &files, builder); err != nil {
		return nil, fmt.Errorf("failed to query files by object type: %w", pgsql.ErrorTransform(err))
	}

	return files, nil
}

// ClearPrimary resets the primary flag for files attached to an entity.
func (r *Repo) ClearPrimary(ctx context.Context, objectType string, objectID int64, excludeID int64) error {
	const op = "filerepo.ClearPrimary"

	if objectType == "" {
		return fmt.Errorf("%s: objectType is required", op)
	}
	if objectID <= 0 {
		return fmt.Errorf("%s: objectID must be positive", op)
	}

	builder := querybuilder.BuilderDollar().
		Update(tableName).
		Set(columnIsPrimary, false).
		Set(columnUpdatedAt, time.Now()).
		Where(squirrel.Eq{columnObjectType: objectType}).
		Where(squirrel.Eq{columnObjectID: objectID}).
		Where(squirrel.Eq{columnIsPrimary: true})

	if excludeID > 0 {
		builder = builder.Where(squirrel.NotEq{columnID: excludeID})
	}

	if _, err := r.pgsql.DB().Execx(ctx, op, builder); err != nil {
		return fmt.Errorf("failed to clear primary flag: %w", pgsql.ErrorTransform(err))
	}

	return nil
}

// SetPrimary marks the specified file as primary and updates its binding.
func (r *Repo) SetPrimary(ctx context.Context, id int64, objectType string, objectID int64) error {
	const op = "filerepo.SetPrimary"

	if id <= 0 {
		return fmt.Errorf("%s: invalid id", op)
	}
	if objectType == "" {
		return fmt.Errorf("%s: objectType is required", op)
	}
	if objectID <= 0 {
		return fmt.Errorf("%s: objectID must be positive", op)
	}

	builder := querybuilder.BuilderDollar().
		Update(tableName).
		SetMap(querybuilder.Eq{
			columnObjectType: objectType,
			columnObjectID:   objectID,
			columnIsPrimary:  true,
			columnUpdatedAt:  time.Now(),
		}).
		Where(squirrel.Eq{columnID: id}).
		Where(squirrel.Eq{columnDeletedAt: nil})

	if _, err := r.pgsql.DB().Execx(ctx, op, builder); err != nil {
		return fmt.Errorf("failed to set primary: %w", pgsql.ErrorTransform(err))
	}

	return nil
}

// UpdatePosition обновляет позицию файла.
func (r *Repo) UpdatePosition(ctx context.Context, id int64, position int) error {
	const op = "filerepo.UpdatePosition"

	if id <= 0 {
		return fmt.Errorf("%s: invalid id", op)
	}

	builder := querybuilder.BuilderDollar().
		Update(tableName).
		Set(columnPosition, position).
		Set(columnUpdatedAt, "NOW()").
		Where(squirrel.Eq{columnID: id}).
		Where(squirrel.Eq{columnDeletedAt: nil})

	if _, err := r.pgsql.DB().Execx(ctx, op, builder); err != nil {
		return fmt.Errorf("failed to update file position: %w", pgsql.ErrorTransform(err))
	}

	return nil
}

// CleanupExpiredFiles удаляет строки, где deleted_at старше, к примеру 60 минут.
// Работает батчами со SKIP LOCKED, чтобы не блокировать рабочие транзакции.
// batchSize: количество строк за раз (например, 100).
// minutes: кол-во минут прошедших с момента удаления.
func (r *Repo) CleanupExpiredFiles(ctx context.Context, batchSize, minutes int) (int64, error) {
	const op = "CleanupExpiredFiles"
	if batchSize <= 0 {
		return 0, fmt.Errorf("%s: invalid batchSize", op)
	}

	if minutes < 0 {
		minutes = 60
	}

	query := fmt.Sprintf(`WITH c AS (
  SELECT id FROM %s
  WHERE deleted_at < $1::timestamp - $2 * INTERVAL '1 minute'
  ORDER BY id ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $3
)
DELETE FROM %s t
USING c
WHERE t.id = c.id;`, tableName, tableName)

	result, err := r.pgsql.DB().Exec(ctx, op, query, time.Now(), minutes, batchSize)
	if err != nil {
		return 0, fmt.Errorf("%s: error cleaning up expired files: %w", op, pgsql.ErrorTransform(err))
	}

	return result.RowsAffected(), nil
}
