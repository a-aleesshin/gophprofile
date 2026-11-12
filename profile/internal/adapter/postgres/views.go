package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const viewColumns = `id, user_id, file_id, status, file_name, mime_type, size_bytes,
	width, height, variants, failed_reason, created_at, updated_at`

func (r *Avatars) ViewByID(
	ctx context.Context,
	id domain.AvatarID,
) (usecase.AvatarView, error) {
	row := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT `+viewColumns+`
		  FROM avatars
		 WHERE id = $1 AND deleted_at IS NULL`, id.UUID())

	view, err := scanAvatarView(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return usecase.AvatarView{}, fmt.Errorf("%w: %s",
			domain.ErrAvatarNotFound, id.String())
	}

	if err != nil {
		return usecase.AvatarView{}, err
	}

	return view, nil
}

func (r *Avatars) ViewsByUser(
	ctx context.Context,
	userID domain.UserID,
	page usecase.Page,
) ([]usecase.AvatarView, error) {
	var (
		after   *time.Time
		afterID *uuid.UUID
	)

	if !page.AfterCreatedAt.IsZero() && !page.AfterID.IsZero() {
		createdAt, id := page.AfterCreatedAt, page.AfterID.UUID()
		after, afterID = &createdAt, &id
	}

	rows, err := r.db.Querier(ctx).Query(ctx, `
		SELECT `+viewColumns+`
		  FROM avatars
		 WHERE user_id = $1
		   AND deleted_at IS NULL
		   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`, userID.String(), after, afterID, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("select avatar views: %w", err)
	}

	defer rows.Close()

	views := make([]usecase.AvatarView, 0, page.Limit)

	for rows.Next() {
		view, scanErr := scanAvatarView(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		views = append(views, view)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate avatar views: %w", err)
	}

	return views, nil
}

func scanAvatarView(row pgx.Row) (usecase.AvatarView, error) {
	var (
		id           uuid.UUID
		userID       string
		fileID       *uuid.UUID
		status       string
		fileName     *string
		mimeType     *string
		sizeBytes    *int64
		width        *int32
		height       *int32
		rawVariants  []byte
		failedReason *string
		view         usecase.AvatarView
	)

	if err := row.Scan(
		&id, &userID, &fileID, &status, &fileName, &mimeType, &sizeBytes,
		&width, &height, &rawVariants, &failedReason, &view.CreatedAt, &view.UpdatedAt,
	); err != nil {
		return usecase.AvatarView{}, err
	}

	view.ID = domain.AvatarIDFromUUID(id)

	var err error

	if view.UserID, err = domain.RestoreUserID(userID); err != nil {
		return usecase.AvatarView{}, err
	}

	if view.Status, err = domain.ParseAvatarStatus(status); err != nil {
		return usecase.AvatarView{}, err
	}

	if fileID != nil {
		view.FileID = domain.FileRefFromUUID(*fileID)
	}

	if fileName != nil {
		view.FileName = *fileName
	}

	if mimeType != nil {
		view.MimeType = *mimeType
	}

	if sizeBytes != nil {
		view.SizeBytes = *sizeBytes
	}

	if width != nil {
		view.Width = *width
	}

	if height != nil {
		view.Height = *height
	}

	if failedReason != nil {
		view.FailedReason = *failedReason
	}

	stored, err := decodeVariants(rawVariants)
	if err != nil {
		return usecase.AvatarView{}, err
	}

	view.Variants = make([]usecase.VariantView, 0, len(stored))

	for _, variant := range stored {
		view.Variants = append(view.Variants, usecase.VariantView{
			Name:     variant.Name,
			Ext:      variant.Ext,
			MimeType: variant.MimeType,
			Width:    variant.Width,
			Height:   variant.Height,
		})
	}

	return view, nil
}
