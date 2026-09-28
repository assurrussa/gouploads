package filerepo

import (
	"context"
	"fmt"

	"github.com/Masterminds/squirrel"
	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	querybuilder "github.com/assurrussa/outbox/shared/query_builder"
	"github.com/georgysavva/scany/v2/pgxscan"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// GetByIDForUpdate must be called in the same transaction as final metadata and
// outbox writes. The caller owns the transaction; this method never starts one.
func (r *Repo) GetByIDForUpdate(ctx context.Context, id int64) (model.File, error) {
	const op = "filerepo.GetByIDForUpdate"
	if id <= 0 {
		return model.File{}, fmt.Errorf("%s: invalid id", op)
	}
	query := querybuilder.BuilderDollar().Select(columns...).From(tableName).
		Where(squirrel.Eq{columnID: id, columnDeletedAt: nil}).Limit(1).Suffix("FOR UPDATE")
	var file model.File
	if err := r.pgsql.DB().ScanOnex(ctx, op, &file, query); err != nil {
		if pgxscan.NotFound(err) {
			return model.File{}, nil
		}
		return model.File{}, fmt.Errorf("%s: %w", op, pgsql.ErrorTransform(err))
	}
	return file, nil
}
