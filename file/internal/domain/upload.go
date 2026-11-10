package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidUpload = errors.New("invalid upload")

type Upload struct {
	id      UploadID
	version int64

	namespace      Namespace
	ownerRef       OwnerRef
	idempotencyKey IdempotencyKey
	originalName   OriginalName
	declaredMime   MimeType
	maxSize        Size
	location       Location
	status         UploadStatus
	fileID         FileID
	expiresAt      time.Time
	createdAt      time.Time
	updatedAt      time.Time
}

type NewUploadParams struct {
	ID             UploadID
	Namespace      Namespace
	OwnerRef       OwnerRef
	IdempotencyKey IdempotencyKey
	OriginalName   OriginalName
	DeclaredMime   MimeType
	MaxSize        Size
	TTL            time.Duration
	Now            time.Time
}

func NewUpload(p NewUploadParams) (Upload, error) {
	switch {
	case p.ID.IsZero():
		return Upload{}, fmt.Errorf("%w: id is required", ErrInvalidUpload)
	case p.Namespace.IsZero():
		return Upload{}, fmt.Errorf("%w: namespace is required", ErrInvalidUpload)
	case p.OwnerRef.IsZero():
		return Upload{}, fmt.Errorf("%w: owner ref is required", ErrInvalidUpload)
	case p.IdempotencyKey.IsZero():
		return Upload{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidUpload)
	case p.OriginalName.IsZero():
		return Upload{}, fmt.Errorf("%w: original name is required", ErrInvalidUpload)
	case p.MaxSize.IsZero():
		return Upload{}, fmt.Errorf("%w: max size is required", ErrInvalidUpload)
	case p.TTL <= 0:
		return Upload{}, fmt.Errorf("%w: ttl must be positive (got %s)", ErrInvalidUpload, p.TTL)
	case p.Now.IsZero():
		return Upload{}, fmt.Errorf("%w: now is required", ErrInvalidUpload)
	}

	location, err := NewStagingLocation(p.ID)
	if err != nil {
		return Upload{}, err
	}

	now := p.Now.UTC()

	return Upload{
		id:             p.ID,
		version:        1,
		namespace:      p.Namespace,
		ownerRef:       p.OwnerRef,
		idempotencyKey: p.IdempotencyKey,
		originalName:   p.OriginalName,
		declaredMime:   p.DeclaredMime,
		maxSize:        p.MaxSize,
		location:       location,
		status:         UploadStatusPending,
		expiresAt:      now.Add(p.TTL),
		createdAt:      now,
		updatedAt:      now,
	}, nil
}

type RestoreUploadParams struct {
	ID             UploadID
	Version        int64
	Namespace      Namespace
	OwnerRef       OwnerRef
	IdempotencyKey IdempotencyKey
	OriginalName   OriginalName
	DeclaredMime   MimeType
	MaxSize        Size
	Location       Location
	Status         UploadStatus
	FileID         FileID
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func RestoreUpload(p RestoreUploadParams) (Upload, error) {
	switch {
	case p.ID.IsZero():
		return Upload{}, fmt.Errorf("%w: id is required", ErrInvalidUpload)
	case p.Version < 1:
		return Upload{}, fmt.Errorf("%w: version must be positive (got %d)", ErrInvalidUpload, p.Version)
	case p.Namespace.IsZero():
		return Upload{}, fmt.Errorf("%w: namespace is required", ErrInvalidUpload)
	case p.OwnerRef.IsZero():
		return Upload{}, fmt.Errorf("%w: owner ref is required", ErrInvalidUpload)
	case p.IdempotencyKey.IsZero():
		return Upload{}, fmt.Errorf("%w: idempotency key is required", ErrInvalidUpload)
	case p.Location.IsZero():
		return Upload{}, fmt.Errorf("%w: location is required", ErrInvalidUpload)
	case !p.Status.IsValid():
		return Upload{}, fmt.Errorf("%w: status %q is not valid", ErrInvalidUpload, p.Status.String())
	case p.ExpiresAt.IsZero():
		return Upload{}, fmt.Errorf("%w: expires at is required", ErrInvalidUpload)
	case p.CreatedAt.IsZero():
		return Upload{}, fmt.Errorf("%w: created at is required", ErrInvalidUpload)
	case p.UpdatedAt.IsZero():
		return Upload{}, fmt.Errorf("%w: updated at is required", ErrInvalidUpload)
	}

	return Upload{
		id:             p.ID,
		version:        p.Version,
		namespace:      p.Namespace,
		ownerRef:       p.OwnerRef,
		idempotencyKey: p.IdempotencyKey,
		originalName:   p.OriginalName,
		declaredMime:   p.DeclaredMime,
		maxSize:        p.MaxSize,
		location:       p.Location,
		status:         p.Status,
		fileID:         p.FileID,
		expiresAt:      p.ExpiresAt.UTC(),
		createdAt:      p.CreatedAt.UTC(),
		updatedAt:      p.UpdatedAt.UTC(),
	}, nil
}

func (u *Upload) Complete(fileID FileID, now time.Time) error {
	next, err := u.prepare(UploadStatusUploaded, now)
	if err != nil {
		return err
	}
	if fileID.IsZero() {
		return fmt.Errorf("%w: file id is required to complete", ErrInvalidUpload)
	}

	u.fileID = fileID
	u.apply(next, now)

	return nil
}

func (u *Upload) Expire(now time.Time) error {
	next, err := u.prepare(UploadStatusExpired, now)
	if err != nil {
		return err
	}

	u.apply(next, now)

	return nil
}

func (u *Upload) Abort(now time.Time) error {
	next, err := u.prepare(UploadStatusAborted, now)
	if err != nil {
		return err
	}

	u.apply(next, now)

	return nil
}

func (u *Upload) SetVersion(version int64) error {
	if version <= u.version {
		return fmt.Errorf(
			"%w: version must grow (have %d, got %d)",
			ErrInvalidUpload, u.version, version,
		)
	}

	u.version = version

	return nil
}

func (u *Upload) prepare(next UploadStatus, now time.Time) (UploadStatus, error) {
	if now.IsZero() {
		return UploadStatus{}, fmt.Errorf("%w: now is required", ErrInvalidUpload)
	}

	return u.status.TransitionTo(next)
}

func (u *Upload) apply(next UploadStatus, now time.Time) {
	u.status = next
	u.updatedAt = now.UTC()
}

func (u *Upload) ID() UploadID { return u.id }

func (u *Upload) Version() int64 { return u.version }

func (u *Upload) Namespace() Namespace { return u.namespace }

func (u *Upload) OwnerRef() OwnerRef { return u.ownerRef }

func (u *Upload) IdempotencyKey() IdempotencyKey { return u.idempotencyKey }

func (u *Upload) OriginalName() OriginalName { return u.originalName }

func (u *Upload) DeclaredMime() MimeType { return u.declaredMime }

func (u *Upload) MaxSize() Size { return u.maxSize }

func (u *Upload) Location() Location { return u.location }

func (u *Upload) Status() UploadStatus { return u.status }

func (u *Upload) FileID() FileID { return u.fileID }

func (u *Upload) ExpiresAt() time.Time { return u.expiresAt }

func (u *Upload) CreatedAt() time.Time { return u.createdAt }

func (u *Upload) UpdatedAt() time.Time { return u.updatedAt }

func (u *Upload) IsPending() bool { return u.status.Equal(UploadStatusPending) }

func (u *Upload) IsExpired(now time.Time) bool {
	return u.IsPending() && now.After(u.expiresAt)
}

func (u *Upload) IsOwnedBy(owner OwnerRef) bool { return u.ownerRef.Equal(owner) }

func (u *Upload) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Upload: serialize via DTO, not directly")
}
