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

const (
	uniqueViolation       = "23505"
	idempotencyConstraint = "uq_uploads_idem"
)

const uploadColumns = `id, version, namespace, owner_ref, idempotency_key, original_name,
	staging_key, declared_mime, max_size_bytes, status, file_id, expires_at, created_at, updated_at`

type Uploads struct {
	db *pgtx.DB
}

func NewUploads(db *pgtx.DB) *Uploads {
	return &Uploads{db: db}
}

func (r *Uploads) Create(ctx context.Context, upload *domain.Upload) error {
	var declaredMime *string

	if !upload.DeclaredMime().IsZero() {
		declaredMime = new(upload.DeclaredMime().String())
	}

	_, err := r.db.Querier(ctx).Exec(ctx, `
		INSERT INTO uploads (
			id, version, namespace, owner_ref, idempotency_key, original_name,
			staging_key, declared_mime, max_size_bytes, status, expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		upload.ID().UUID(),
		upload.Version(),
		upload.Namespace().String(),
		upload.OwnerRef().String(),
		upload.IdempotencyKey().String(),
		upload.OriginalName().String(),
		upload.Location().String(),
		declaredMime,
		upload.MaxSize().Bytes(),
		upload.Status().String(),
		upload.ExpiresAt(),
		upload.CreatedAt(),
		upload.UpdatedAt(),
	)

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
		pgErr.Code == uniqueViolation &&
		pgErr.ConstraintName == idempotencyConstraint {
		return fmt.Errorf("%w: %s", domain.ErrUploadAlreadyExists, pgErr.ConstraintName)
	}

	if err != nil {
		return fmt.Errorf("insert upload: %w", err)
	}

	return nil
}

func (r *Uploads) ByID(ctx context.Context, id domain.UploadID) (domain.Upload, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+uploadColumns+` FROM uploads WHERE id = $1`, id.UUID())

	upload, err := scanUpload(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Upload{}, fmt.Errorf("%w: %s", domain.ErrUploadNotFound, id.String())
	}

	if err != nil {
		return domain.Upload{}, err
	}

	return upload, nil
}

func (r *Uploads) ByIdempotencyKey(
	ctx context.Context,
	namespace domain.Namespace,
	key domain.IdempotencyKey,
) (domain.Upload, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+uploadColumns+` FROM uploads WHERE namespace = $1 AND idempotency_key = $2`,
		namespace.String(), key.String())

	upload, err := scanUpload(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Upload{}, fmt.Errorf("%w: %s", domain.ErrUploadNotFound, key.String())
	}

	if err != nil {
		return domain.Upload{}, err
	}

	return upload, nil
}

func (r *Uploads) PendingExpiredBefore(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]domain.Upload, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT `+uploadColumns+` FROM uploads
		 WHERE status = 'pending' AND expires_at < $1
		 ORDER BY expires_at
		 LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending uploads: %w", err)
	}

	defer rows.Close()

	uploads := make([]domain.Upload, 0, limit)

	for rows.Next() {
		upload, scanErr := scanUpload(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		uploads = append(uploads, upload)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending uploads: %w", err)
	}

	return uploads, nil
}

func (r *Uploads) Save(ctx context.Context, upload *domain.Upload) error {
	var fileID *uuid.UUID

	if !upload.FileID().IsZero() {
		fileID = new(upload.FileID().UUID())
	}

	var version int64

	err := r.db.Querier(ctx).QueryRow(ctx, `
		UPDATE uploads
		   SET version = version + 1,
		       status = $3,
		       file_id = $4,
		       updated_at = $5
		 WHERE id = $1 AND version = $2
		RETURNING version`,
		upload.ID().UUID(),
		upload.Version(),
		upload.Status().String(),
		fileID,
		upload.UpdatedAt(),
	).Scan(&version)

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: upload %s", usecase.ErrStaleVersion, upload.ID().String())
	}

	if err != nil {
		return fmt.Errorf("update upload: %w", err)
	}

	return upload.SetVersion(version)
}

func scanUpload(row pgx.Row) (domain.Upload, error) {
	var (
		id             uuid.UUID
		version        int64
		namespace      string
		ownerRef       string
		idempotencyKey string
		originalName   string
		stagingKey     string
		declaredMime   *string
		maxSizeBytes   int64
		status         string
		fileID         *uuid.UUID
		expiresAt      time.Time
		createdAt      time.Time
		updatedAt      time.Time
	)

	if err := row.Scan(
		&id, &version, &namespace, &ownerRef, &idempotencyKey, &originalName,
		&stagingKey, &declaredMime, &maxSizeBytes, &status, &fileID,
		&expiresAt, &createdAt, &updatedAt,
	); err != nil {
		return domain.Upload{}, err
	}

	params := domain.RestoreUploadParams{
		ID:        domain.UploadIDFromUUID(id),
		Version:   version,
		ExpiresAt: expiresAt,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	var err error

	if params.Namespace, err = domain.RestoreNamespace(namespace); err != nil {
		return domain.Upload{}, err
	}

	if params.OwnerRef, err = domain.RestoreOwnerRef(ownerRef); err != nil {
		return domain.Upload{}, err
	}

	if params.IdempotencyKey, err = domain.RestoreIdempotencyKey(idempotencyKey); err != nil {
		return domain.Upload{}, err
	}

	if params.OriginalName, err = domain.RestoreOriginalName(originalName); err != nil {
		return domain.Upload{}, err
	}

	if params.Location, err = domain.RestoreLocation(stagingKey); err != nil {
		return domain.Upload{}, err
	}

	if params.Status, err = domain.ParseUploadStatus(status); err != nil {
		return domain.Upload{}, err
	}

	if params.MaxSize, err = domain.NewSize(maxSizeBytes); err != nil {
		return domain.Upload{}, err
	}

	if declaredMime != nil {
		if params.DeclaredMime, err = domain.RestoreMimeType(*declaredMime); err != nil {
			return domain.Upload{}, err
		}
	}

	if fileID != nil {
		params.FileID = domain.FileIDFromUUID(*fileID)
	}

	return domain.RestoreUpload(params)
}
