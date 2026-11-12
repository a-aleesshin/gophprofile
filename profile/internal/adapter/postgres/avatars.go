package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const avatarColumns = `id, version, user_id, upload_id, file_id, status, file_name, mime_type,
	size_bytes, width, height, variants, failed_reason, created_at, updated_at, deleted_at`

type variantRow struct {
	Name     string `json:"name"`
	Ext      string `json:"ext"`
	MimeType string `json:"mime_type"`
	Width    int32  `json:"width"`
	Height   int32  `json:"height"`
}

type Avatars struct {
	db *pgtx.DB
}

func NewAvatars(db *pgtx.DB) *Avatars {
	return &Avatars{db: db}
}

func (r *Avatars) Create(ctx context.Context, avatar *domain.Avatar) error {
	var uploadID *uuid.UUID

	if !avatar.UploadID().IsZero() {
		value := avatar.UploadID().UUID()
		uploadID = &value
	}

	_, err := r.db.Querier(ctx).Exec(ctx, `
		INSERT INTO avatars (
			id, version, user_id, upload_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		avatar.ID().UUID(),
		avatar.Version(),
		avatar.UserID().String(),
		uploadID,
		avatar.Status().String(),
		avatar.CreatedAt(),
		avatar.UpdatedAt(),
	)

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return fmt.Errorf("%w: %s", domain.ErrAvatarExists, pgErr.ConstraintName)
	}

	if err != nil {
		return fmt.Errorf("insert avatar: %w", err)
	}

	return nil
}

func (r *Avatars) Save(ctx context.Context, avatar *domain.Avatar) error {
	var (
		fileID       *uuid.UUID
		fileName     *string
		mimeType     *string
		sizeBytes    *int64
		width        *int32
		height       *int32
		failedReason *string
		deletedAt    *time.Time
	)

	if !avatar.FileID().IsZero() {
		value := avatar.FileID().UUID()
		fileID = &value
	}

	if snapshot := avatar.Snapshot(); !snapshot.IsZero() {
		name, mime, size := snapshot.FileName(), snapshot.MimeType(), snapshot.SizeBytes()
		fileName, mimeType, sizeBytes = &name, &mime, &size

		if snapshot.Width() > 0 && snapshot.Height() > 0 {
			w, h := snapshot.Width(), snapshot.Height()
			width, height = &w, &h
		}
	}

	if avatar.FailedReason() != "" {
		value := avatar.FailedReason()
		failedReason = &value
	}

	if !avatar.DeletedAt().IsZero() {
		value := avatar.DeletedAt()
		deletedAt = &value
	}

	variants, err := encodeVariants(avatar.Variants())
	if err != nil {
		return err
	}

	var version int64

	err = r.db.Querier(ctx).QueryRow(ctx, `
		UPDATE avatars
		   SET version = version + 1,
		       file_id = $3,
		       status = $4,
		       file_name = $5,
		       mime_type = $6,
		       size_bytes = $7,
		       width = $8,
		       height = $9,
		       variants = $10,
		       failed_reason = $11,
		       updated_at = $12,
		       deleted_at = $13
		 WHERE id = $1 AND version = $2
		RETURNING version`,
		avatar.ID().UUID(),
		avatar.Version(),
		fileID,
		avatar.Status().String(),
		fileName,
		mimeType,
		sizeBytes,
		width,
		height,
		variants,
		failedReason,
		avatar.UpdatedAt(),
		deletedAt,
	).Scan(&version)

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return fmt.Errorf("%w: %s", domain.ErrAvatarExists, pgErr.ConstraintName)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: avatar %s", usecase.ErrStaleVersion, avatar.ID().String())
	}

	if err != nil {
		return fmt.Errorf("update avatar: %w", err)
	}

	return avatar.SetVersion(version)
}

func (r *Avatars) ByID(ctx context.Context, id domain.AvatarID) (domain.Avatar, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+avatarColumns+` FROM avatars WHERE id = $1`, id.UUID())

	avatar, err := scanAvatar(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Avatar{}, fmt.Errorf("%w: %s", domain.ErrAvatarNotFound, id.String())
	}

	if err != nil {
		return domain.Avatar{}, err
	}

	return avatar, nil
}

func (r *Avatars) ByFileID(
	ctx context.Context,
	fileID domain.FileRef,
) (domain.Avatar, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+avatarColumns+` FROM avatars WHERE file_id = $1`, fileID.UUID())

	avatar, err := scanAvatar(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Avatar{}, fmt.Errorf("%w: file %s",
			domain.ErrAvatarNotFound, fileID.String())
	}

	if err != nil {
		return domain.Avatar{}, err
	}

	return avatar, nil
}

func (r *Avatars) CountByUser(ctx context.Context, userID domain.UserID) (int, error) {
	var count int

	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT count(*) FROM avatars
		 WHERE user_id = $1 AND deleted_at IS NULL`, userID.String()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count avatars: %w", err)
	}

	return count, nil
}

func (r *Avatars) StaleByStatus(
	ctx context.Context,
	statuses []domain.AvatarStatus,
	updatedBefore time.Time,
	limit int,
) ([]domain.Avatar, error) {
	raw := make([]string, 0, len(statuses))
	for _, status := range statuses {
		raw = append(raw, status.String())
	}

	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+avatarColumns+`
		  FROM avatars
		 WHERE status = ANY($1) AND updated_at < $2 AND deleted_at IS NULL
		 ORDER BY updated_at
		 LIMIT $3
		   FOR UPDATE SKIP LOCKED`, raw, updatedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("select stale avatars: %w", err)
	}

	defer rows.Close()

	avatars := make([]domain.Avatar, 0, limit)

	for rows.Next() {
		avatar, scanErr := scanAvatar(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		avatars = append(avatars, avatar)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale avatars: %w", err)
	}

	return avatars, nil
}

func encodeVariants(variants []domain.Variant) ([]byte, error) {
	if len(variants) == 0 {
		return nil, nil
	}

	rows := make([]variantRow, 0, len(variants))

	for _, variant := range variants {
		rows = append(rows, variantRow{
			Name:     variant.Name(),
			Ext:      variant.Ext(),
			MimeType: variant.MimeType(),
			Width:    variant.Width(),
			Height:   variant.Height(),
		})
	}

	encoded, err := json.Marshal(rows)
	if err != nil {
		return nil, fmt.Errorf("encode variants: %w", err)
	}

	return encoded, nil
}

func decodeVariants(raw []byte) ([]variantRow, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var rows []variantRow

	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode variants: %w", err)
	}

	return rows, nil
}

func scanAvatar(row pgx.Row) (domain.Avatar, error) {
	var (
		id           uuid.UUID
		version      int64
		userID       string
		uploadID     *uuid.UUID
		fileID       *uuid.UUID
		status       string
		fileName     *string
		mimeType     *string
		sizeBytes    *int64
		width        *int32
		height       *int32
		rawVariants  []byte
		failedReason *string
		createdAt    time.Time
		updatedAt    time.Time
		deletedAt    *time.Time
	)

	if err := row.Scan(
		&id, &version, &userID, &uploadID, &fileID, &status, &fileName, &mimeType,
		&sizeBytes, &width, &height, &rawVariants, &failedReason,
		&createdAt, &updatedAt, &deletedAt,
	); err != nil {
		return domain.Avatar{}, err
	}

	params := domain.RestoreAvatarParams{
		ID:        domain.AvatarIDFromUUID(id),
		Version:   version,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	var err error

	if params.UserID, err = domain.RestoreUserID(userID); err != nil {
		return domain.Avatar{}, err
	}

	if params.Status, err = domain.ParseAvatarStatus(status); err != nil {
		return domain.Avatar{}, err
	}

	if uploadID != nil {
		params.UploadID = domain.UploadRefFromUUID(*uploadID)
	}

	if fileID != nil {
		params.FileID = domain.FileRefFromUUID(*fileID)
	}

	if mimeType != nil && sizeBytes != nil {
		snapshot := domain.NewFileSnapshotParams{MimeType: *mimeType, SizeBytes: *sizeBytes}

		if fileName != nil {
			snapshot.FileName = *fileName
		}

		if width != nil && height != nil {
			snapshot.Width, snapshot.Height = *width, *height
		}

		if params.Snapshot, err = domain.NewFileSnapshot(snapshot); err != nil {
			return domain.Avatar{}, err
		}
	}

	rows, err := decodeVariants(rawVariants)
	if err != nil {
		return domain.Avatar{}, err
	}

	params.Variants = make([]domain.Variant, 0, len(rows))

	for _, stored := range rows {
		variant, buildErr := domain.NewVariant(domain.NewVariantParams{
			Name:     stored.Name,
			Ext:      stored.Ext,
			MimeType: stored.MimeType,
			Width:    stored.Width,
			Height:   stored.Height,
		})
		if buildErr != nil {
			return domain.Avatar{}, buildErr
		}

		params.Variants = append(params.Variants, variant)
	}

	if failedReason != nil {
		params.FailedReason = *failedReason
	}

	if deletedAt != nil {
		params.DeletedAt = *deletedAt
	}

	return domain.RestoreAvatar(params)
}
