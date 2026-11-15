package postgres

import (
	"context"
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

const uniqueViolation = "23505"

const profileColumns = `user_id, version, email, email_sha256, current_avatar_id,
	created_at, updated_at`

type Profiles struct {
	db *pgtx.DB
}

func NewProfiles(db *pgtx.DB) *Profiles {
	return &Profiles{db: db}
}

func (r *Profiles) Create(ctx context.Context, profile *domain.Profile) error {
	var (
		email     *string
		emailHash *string
		current   *uuid.UUID
	)

	if profile.HasEmail() {
		value, hash := profile.Email().String(), profile.EmailHash().String()
		email, emailHash = &value, &hash
	}

	if !profile.CurrentAvatarID().IsZero() {
		value := profile.CurrentAvatarID().UUID()
		current = &value
	}

	var version int64

	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO profiles (
			user_id, version, email, email_sha256, current_avatar_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO NOTHING
		RETURNING version`,
		profile.UserID().String(),
		profile.Version(),
		email,
		emailHash,
		current,
		profile.CreatedAt(),
		profile.UpdatedAt(),
	).Scan(&version)

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return fmt.Errorf("%w: %s", domain.ErrEmailTaken, pgErr.ConstraintName)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: user %s", domain.ErrProfileExists, profile.UserID().String())
	}

	if err != nil {
		return fmt.Errorf("insert profile: %w", err)
	}

	return profile.SetVersion(version)
}

func (r *Profiles) Save(ctx context.Context, profile *domain.Profile) error {
	var (
		email     *string
		emailHash *string
		current   *uuid.UUID
	)

	if profile.HasEmail() {
		value, hash := profile.Email().String(), profile.EmailHash().String()
		email, emailHash = &value, &hash
	}

	if !profile.CurrentAvatarID().IsZero() {
		value := profile.CurrentAvatarID().UUID()
		current = &value
	}

	var version int64

	err := r.db.Querier(ctx).QueryRow(ctx, `
		UPDATE profiles
		   SET version = version + 1,
		       email = $3,
		       email_sha256 = $4,
		       current_avatar_id = $5,
		       updated_at = $6
		 WHERE user_id = $1 AND version = $2
		RETURNING version`,
		profile.UserID().String(),
		profile.Version(),
		email,
		emailHash,
		current,
		profile.UpdatedAt(),
	).Scan(&version)

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
		return fmt.Errorf("%w: %s", domain.ErrEmailTaken, pgErr.ConstraintName)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: profile %s", usecase.ErrStaleVersion, profile.UserID().String())
	}

	if err != nil {
		return fmt.Errorf("update profile: %w", err)
	}

	return profile.SetVersion(version)
}

func (r *Profiles) ByUserID(
	ctx context.Context,
	userID domain.UserID,
) (domain.Profile, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE user_id = $1`, userID.String())

	profile, err := scanProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, fmt.Errorf("%w: user %s",
			domain.ErrProfileNotFound, userID.String())
	}

	if err != nil {
		return domain.Profile{}, err
	}

	return profile, nil
}

func (r *Profiles) ByEmailHash(
	ctx context.Context,
	hash domain.EmailHash,
) (domain.Profile, error) {
	row := r.db.Querier(ctx).QueryRow(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE email_sha256 = $1`, hash.String())

	profile, err := scanProfile(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{}, fmt.Errorf("%w: email hash %s",
			domain.ErrProfileNotFound, hash.String())
	}

	if err != nil {
		return domain.Profile{}, err
	}

	return profile, nil
}

func scanProfile(row pgx.Row) (domain.Profile, error) {
	var (
		userID    string
		version   int64
		email     *string
		emailHash *string
		current   *uuid.UUID
		createdAt time.Time
		updatedAt time.Time
	)

	if err := row.Scan(
		&userID, &version, &email, &emailHash, &current, &createdAt, &updatedAt,
	); err != nil {
		return domain.Profile{}, err
	}

	params := domain.RestoreProfileParams{
		Version:   version,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	var err error

	if params.UserID, err = domain.RestoreUserID(userID); err != nil {
		return domain.Profile{}, err
	}

	if email != nil {
		if params.Email, err = domain.RestoreEmail(*email); err != nil {
			return domain.Profile{}, err
		}
	}

	if emailHash != nil {
		if params.EmailHash, err = domain.RestoreEmailHash(*emailHash); err != nil {
			return domain.Profile{}, err
		}
	}

	if current != nil {
		params.CurrentAvatarID = domain.AvatarIDFromUUID(*current)
	}

	return domain.RestoreProfile(params)
}
