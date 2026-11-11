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

const fileColumns = `id, version, namespace, owner_ref, original_name, mime_type, size_bytes,
	checksum_sha256, width, height, storage_key, status, reject_reason,
	created_at, updated_at, deleted_at`

type Files struct {
	db *pgtx.DB
}

func NewFiles(db *pgtx.DB) *Files {
	return &Files{db: db}
}

func (r *Files) Create(ctx context.Context, file *domain.File) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `
		INSERT INTO files (
			id, version, namespace, owner_ref, original_name, mime_type, size_bytes,
			storage_key, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		file.ID().UUID(),
		file.Version(),
		file.Namespace().String(),
		file.OwnerRef().String(),
		file.OriginalName().String(),
		file.MimeType().String(),
		file.Size().Bytes(),
		file.Location().String(),
		file.Status().String(),
		file.CreatedAt(),
		file.UpdatedAt(),
	)

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return fmt.Errorf("%w: %s", domain.ErrFileAlreadyExists, pgErr.ConstraintName)
	}

	if err != nil {
		return fmt.Errorf("insert file: %w", err)
	}

	return nil
}

func (r *Files) ByID(ctx context.Context, id domain.FileID) (domain.File, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+fileColumns+` FROM files WHERE id = $1`, id.UUID())

	file, err := scanFile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.File{}, fmt.Errorf("%w: %s", domain.ErrFileNotFound, id.String())
	}

	if err != nil {
		return domain.File{}, err
	}

	return file, nil
}

func (r *Files) Save(ctx context.Context, file *domain.File) error {
	var (
		checksum     *string
		width        *int32
		height       *int32
		rejectReason *string
		deletedAt    *time.Time
	)

	if !file.Checksum().IsZero() {
		value := file.Checksum().String()
		checksum = &value
	}

	if !file.Dimensions().IsZero() {
		w, h := file.Dimensions().Width(), file.Dimensions().Height()
		width, height = &w, &h
	}

	if !file.RejectReason().IsZero() {
		value := file.RejectReason().String()
		rejectReason = &value
	}

	if value, ok := file.DeletedAt(); ok {
		deletedAt = &value
	}

	var version int64

	err := r.db.Querier(ctx).QueryRow(ctx, `
		UPDATE files
		   SET version = version + 1,
		       checksum_sha256 = $3,
		       width = $4,
		       height = $5,
		       status = $6,
		       reject_reason = $7,
		       updated_at = $8,
		       deleted_at = $9
		 WHERE id = $1 AND version = $2
		RETURNING version`,
		file.ID().UUID(),
		file.Version(),
		checksum,
		width,
		height,
		file.Status().String(),
		rejectReason,
		file.UpdatedAt(),
		deletedAt,
	).Scan(&version)

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: file %s", usecase.ErrStaleVersion, file.ID().String())
	}

	if err != nil {
		return fmt.Errorf("update file: %w", err)
	}

	return file.SetVersion(version)
}

func (r *Files) ViewsByIDs(
	ctx context.Context,
	ids []domain.FileID,
) ([]usecase.FileView, error) {
	raw := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, id.UUID())
	}

	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT id, namespace, owner_ref, original_name, mime_type, size_bytes,
		       width, height, status, reject_reason, created_at, updated_at
		  FROM files
		 WHERE id = ANY($1) AND deleted_at IS NULL`, raw)
	if err != nil {
		return nil, fmt.Errorf("select file views: %w", err)
	}

	defer rows.Close()

	views := make([]usecase.FileView, 0, len(ids))

	for rows.Next() {
		var (
			id           uuid.UUID
			namespace    string
			status       string
			rejectReason *string
			width        *int32
			height       *int32
			view         usecase.FileView
		)

		if err := rows.Scan(
			&id, &namespace, &view.OwnerRef, &view.OriginalName, &view.MimeType,
			&view.SizeBytes, &width, &height, &status, &rejectReason,
			&view.CreatedAt, &view.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan file view: %w", err)
		}

		view.ID = domain.FileIDFromUUID(id)

		if view.Namespace, err = domain.RestoreNamespace(namespace); err != nil {
			return nil, err
		}

		if view.Status, err = domain.ParseFileStatus(status); err != nil {
			return nil, err
		}

		if width != nil {
			view.Width = *width
		}

		if height != nil {
			view.Height = *height
		}

		if rejectReason != nil {
			view.RejectReason = *rejectReason
		}

		views = append(views, view)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file views: %w", err)
	}

	return views, nil
}

func (r *Files) StaleByStatus(
	ctx context.Context,
	statuses []domain.FileStatus,
	updatedBefore time.Time,
	limit int,
) ([]domain.File, error) {
	raw := make([]string, 0, len(statuses))
	for _, status := range statuses {
		raw = append(raw, status.String())
	}

	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+fileColumns+`
		  FROM files
		 WHERE status = ANY($1) AND updated_at < $2
		 ORDER BY updated_at
		 LIMIT $3`, raw, updatedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("select stale files: %w", err)
	}

	defer rows.Close()

	files := make([]domain.File, 0, limit)

	for rows.Next() {
		file, scanErr := scanFile(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		files = append(files, file)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale files: %w", err)
	}

	return files, nil
}

func scanFile(row pgx.Row) (domain.File, error) {
	var (
		id           uuid.UUID
		version      int64
		namespace    string
		ownerRef     string
		originalName string
		mimeType     string
		sizeBytes    int64
		checksum     *string
		width        *int32
		height       *int32
		storageKey   string
		status       string
		rejectReason *string
		createdAt    time.Time
		updatedAt    time.Time
		deletedAt    *time.Time
	)

	if err := row.Scan(
		&id, &version, &namespace, &ownerRef, &originalName, &mimeType, &sizeBytes,
		&checksum, &width, &height, &storageKey, &status, &rejectReason,
		&createdAt, &updatedAt, &deletedAt,
	); err != nil {
		return domain.File{}, err
	}

	params := domain.RestoreFileParams{
		ID:        domain.FileIDFromUUID(id),
		Version:   version,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		DeletedAt: deletedAt,
	}

	var err error

	if params.Namespace, err = domain.RestoreNamespace(namespace); err != nil {
		return domain.File{}, err
	}

	if params.OwnerRef, err = domain.RestoreOwnerRef(ownerRef); err != nil {
		return domain.File{}, err
	}

	if params.OriginalName, err = domain.RestoreOriginalName(originalName); err != nil {
		return domain.File{}, err
	}

	if params.MimeType, err = domain.RestoreMimeType(mimeType); err != nil {
		return domain.File{}, err
	}

	if params.Size, err = domain.NewSize(sizeBytes); err != nil {
		return domain.File{}, err
	}

	if params.Location, err = domain.RestoreLocation(storageKey); err != nil {
		return domain.File{}, err
	}

	if params.Status, err = domain.ParseFileStatus(status); err != nil {
		return domain.File{}, err
	}

	if checksum != nil {
		if params.Checksum, err = domain.RestoreChecksum(*checksum); err != nil {
			return domain.File{}, err
		}
	}

	if width != nil && height != nil {
		if params.Dimensions, err = domain.RestoreDimensions(*width, *height); err != nil {
			return domain.File{}, err
		}
	}

	if rejectReason != nil {
		if params.RejectReason, err = domain.ParseRejectReason(*rejectReason); err != nil {
			return domain.File{}, err
		}
	}

	return domain.RestoreFile(params)
}
