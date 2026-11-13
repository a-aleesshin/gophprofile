package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

type Variants struct {
	db *pgtx.DB
}

func NewVariants(db *pgtx.DB) *Variants {
	return &Variants{db: db}
}

func (r *Variants) CreateAll(ctx context.Context, variants []domain.Variant) (err error) {
	if len(variants) == 0 {
		return nil
	}

	batch := &pgx.Batch{}

	for _, variant := range variants {
		var width, height *int32

		if !variant.Dimensions().IsZero() {
			width, height = new(variant.Dimensions().Width()), new(variant.Dimensions().Height())
		}

		batch.Queue(`
			INSERT INTO file_variants (
				file_id, name, ext, mime_type, width, height,
				size_bytes, checksum_sha256, storage_key, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			variant.FileID().UUID(),
			variant.Name().String(),
			variant.Ext().String(),
			variant.MimeType().String(),
			width,
			height,
			variant.Size().Bytes(),
			variant.Checksum().String(),
			variant.Location().String(),
			variant.CreatedAt(),
		)
	}

	results := r.db.Querier(ctx).SendBatch(ctx, batch)

	defer func() {
		if closeErr := results.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close variant batch: %w", closeErr)
		}
	}()

	for i := range variants {
		if _, execErr := results.Exec(); execErr != nil {
			if pgErr, ok := errors.AsType[*pgconn.PgError](execErr); ok &&
				pgErr.Code == uniqueViolation {
				return fmt.Errorf("%w: %s %s",
					domain.ErrVariantAlreadyExists, variants[i].Name().String(), pgErr.ConstraintName)
			}

			return fmt.Errorf("insert variant %q: %w", variants[i].Name().String(), execErr)
		}
	}

	return nil
}

func (r *Variants) LocationsByFileID(
	ctx context.Context,
	id domain.FileID,
) ([]domain.Location, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT storage_key FROM file_variants WHERE file_id = $1 ORDER BY name`, id.UUID())
	if err != nil {
		return nil, fmt.Errorf("select variant locations: %w", err)
	}

	defer rows.Close()

	locations := make([]domain.Location, 0, 4)

	for rows.Next() {
		var storageKey string

		if err := rows.Scan(&storageKey); err != nil {
			return nil, fmt.Errorf("scan variant location: %w", err)
		}

		location, locErr := domain.RestoreLocation(storageKey)
		if locErr != nil {
			return nil, locErr
		}

		locations = append(locations, location)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate variant locations: %w", err)
	}

	return locations, nil
}

func (r *Variants) ViewsByFileIDs(
	ctx context.Context,
	ids []domain.FileID,
) (map[domain.FileID][]usecase.VariantView, error) {
	raw := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, id.UUID())
	}

	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT file_id, name, ext, mime_type, width, height,
		       size_bytes, checksum_sha256, created_at
		  FROM file_variants
		 WHERE file_id = ANY($1)
		 ORDER BY file_id, name`, raw)
	if err != nil {
		return nil, fmt.Errorf("select variant views: %w", err)
	}

	defer rows.Close()

	views := make(map[domain.FileID][]usecase.VariantView, len(ids))

	for rows.Next() {
		var (
			fileID uuid.UUID
			width  *int32
			height *int32
			view   usecase.VariantView
		)

		if err := rows.Scan(
			&fileID, &view.Name, &view.Ext, &view.MimeType,
			&width, &height, &view.SizeBytes, &view.ETag, &view.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan variant view: %w", err)
		}

		if width != nil {
			view.Width = *width
		}

		if height != nil {
			view.Height = *height
		}

		key := domain.FileIDFromUUID(fileID)
		views[key] = append(views[key], view)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate variant views: %w", err)
	}

	return views, nil
}

func (r *Variants) ServableByName(
	ctx context.Context,
	fileID domain.FileID,
	name domain.VariantName,
) (usecase.ServableVariantView, error) {
	var (
		namespace  string
		storageKey string
		view       usecase.ServableVariantView
		createdAt  time.Time
	)

	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT f.namespace, v.storage_key, v.ext, v.mime_type,
		       v.size_bytes, v.checksum_sha256, v.created_at
		  FROM file_variants v
		  JOIN files f ON f.id = v.file_id
		 WHERE v.file_id = $1
		   AND v.name = $2
		   AND f.status = 'ready'
		   AND f.deleted_at IS NULL`, fileID.UUID(), name.String(),
	).Scan(&namespace, &storageKey, &view.Ext, &view.MimeType,
		&view.SizeBytes, &view.ETag, &createdAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.ServableVariantView{}, fmt.Errorf(
			"%w: %s/%s", domain.ErrVariantAbsent, fileID.String(), name.String(),
		)
	}

	if err != nil {
		return usecase.ServableVariantView{}, fmt.Errorf("select servable variant: %w", err)
	}

	view.CreatedAt = createdAt

	if view.Namespace, err = domain.RestoreNamespace(namespace); err != nil {
		return usecase.ServableVariantView{}, err
	}

	if view.Location, err = domain.RestoreLocation(storageKey); err != nil {
		return usecase.ServableVariantView{}, err
	}

	return view, nil
}

func (r *Variants) LinkTargetByName(
	ctx context.Context,
	fileID domain.FileID,
	name domain.VariantName,
) (usecase.LinkTargetView, error) {
	var (
		namespace string
		status    string
		view      usecase.LinkTargetView
	)

	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT f.namespace, v.name, v.ext, f.status, f.deleted_at IS NOT NULL
		  FROM file_variants v
		  JOIN files f ON f.id = v.file_id
		 WHERE v.file_id = $1 AND v.name = $2`, fileID.UUID(), name.String(),
	).Scan(&namespace, &view.Variant, &view.Ext, &status, &view.Deleted)

	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.LinkTargetView{}, fmt.Errorf(
			"%w: %s/%s", domain.ErrVariantAbsent, fileID.String(), name.String(),
		)
	}

	if err != nil {
		return usecase.LinkTargetView{}, fmt.Errorf("select link target: %w", err)
	}

	if view.Namespace, err = domain.RestoreNamespace(namespace); err != nil {
		return usecase.LinkTargetView{}, err
	}

	if view.Status, err = domain.ParseFileStatus(status); err != nil {
		return usecase.LinkTargetView{}, err
	}

	return view, nil
}
