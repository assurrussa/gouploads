package filerepo

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	querybuilder "github.com/assurrussa/goshared/pkg/query_builder"
	pgsql "github.com/assurrussa/outbox/infrastructure/pgsql/storage"
	"github.com/georgysavva/scany/v2/pgxscan"

	"github.com/assurrussa/gouploads/domain/files/model"
)

const (
	tableName = "files"
)

var columns = []string{
	"id", "user_id", "manager_id", "object_type", "object_id", "original_filename",
	"filename", "folder_path", "provider", "size", "mime_type",
	"moderate", "block_cause", "name", "description", "file_type", "position", "is_primary", "url",
	"slug", "locale", "data", "created_at", "updated_at", "deleted_at", "published_at",
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
		Select("count(id) as total").
		From(tableName).
		Where(squirrel.Eq{"deleted_at": nil})

	// Базовый запрос для получения данных
	listBuilder := querybuilder.BuilderDollar().
		Select(columns...).
		From(tableName).
		Where(squirrel.Eq{"deleted_at": nil})

	// Применяем фильтры
	if filters.FileType != "" {
		countBuilder = countBuilder.Where(squirrel.Like{"mime_type": filters.FileType + "%"})
		listBuilder = listBuilder.Where(squirrel.Like{"mime_type": filters.FileType + "%"})
	}

	if filters.ObjectType != "" {
		countBuilder = countBuilder.Where(squirrel.Eq{"object_type": filters.ObjectType})
		listBuilder = listBuilder.Where(squirrel.Eq{"object_type": filters.ObjectType})
	}

	if filters.ObjectID > 0 {
		countBuilder = countBuilder.Where(squirrel.Eq{"object_id": filters.ObjectID})
		listBuilder = listBuilder.Where(squirrel.Eq{"object_id": filters.ObjectID})
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
			"user_id":           file.UserID,
			"manager_id":        file.ManagerID,
			"object_type":       file.ObjectType,
			"object_id":         file.ObjectID,
			"original_filename": file.OriginalFileName,
			"filename":          file.FileName,
			"folder_path":       file.FolderPath,
			"provider":          file.Provider,
			"size":              file.Size,
			"mime_type":         file.MimeType,
			"url":               file.URL,
			"slug":              file.Slug,
			"name":              file.Name,
			"description":       file.Description,
			"data":              file.Data,
			"is_primary":        file.IsPrimary,
			"created_at":        file.CreatedAt,
			"updated_at":        file.UpdatedAt,
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
			"user_id":           file.UserID,
			"manager_id":        file.ManagerID,
			"object_type":       file.ObjectType,
			"object_id":         file.ObjectID,
			"original_filename": file.OriginalFileName,
			"filename":          file.FileName,
			"folder_path":       file.FolderPath,
			"provider":          file.Provider,
			"size":              file.Size,
			"mime_type":         file.MimeType,
			"url":               file.URL,
			"name":              file.Name,
			"data":              file.Data,
			"description":       file.Description,
			"is_primary":        file.IsPrimary,
			"created_at":        file.CreatedAt,
			"updated_at":        file.UpdatedAt,
		}).Where(squirrel.Eq{"id": id})

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
		Where(squirrel.Eq{"id": id, "deleted_at": nil}).
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
		Set("deleted_at", time.Now()).
		Where(squirrel.Eq{"id": id}).
		Where(squirrel.Eq{"deleted_at": nil})

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
		Where(squirrel.Eq{"object_type": objectType}).
		Where(squirrel.Eq{"object_id": objectID}).
		Where(squirrel.Eq{"deleted_at": nil}).
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
		Set("is_primary", false).
		Set("updated_at", time.Now()).
		Where(squirrel.Eq{"object_type": objectType}).
		Where(squirrel.Eq{"object_id": objectID}).
		Where(squirrel.Eq{"is_primary": true})

	if excludeID > 0 {
		builder = builder.Where(squirrel.NotEq{"id": excludeID})
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
			"object_type": objectType,
			"object_id":   objectID,
			"is_primary":  true,
			"updated_at":  time.Now(),
		}).
		Where(squirrel.Eq{"id": id}).
		Where(squirrel.Eq{"deleted_at": nil})

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
		Set("position", position).
		Set("updated_at", "NOW()").
		Where(squirrel.Eq{"id": id}).
		Where(squirrel.Eq{"deleted_at": nil})

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
