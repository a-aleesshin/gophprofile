package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidAvatar  = errors.New("invalid avatar")
	ErrAvatarNotFound = errors.New("avatar not found")
	ErrAvatarExists   = errors.New("avatar already exists")
)

const maxVariantsPerAvatar = 16

type Avatar struct {
	id           AvatarID
	version      int64
	userID       UserID
	uploadID     UploadRef
	fileID       FileRef
	status       AvatarStatus
	snapshot     FileSnapshot
	variants     []Variant
	failedReason string
	createdAt    time.Time
	updatedAt    time.Time
	deletedAt    time.Time
}

type NewAvatarParams struct {
	ID       AvatarID
	UserID   UserID
	UploadID UploadRef
	Now      time.Time
}

func NewAvatar(p NewAvatarParams) (Avatar, error) {
	switch {
	case p.ID.IsZero():
		return Avatar{}, fmt.Errorf("%w: id is required", ErrInvalidAvatar)
	case p.UserID.IsZero():
		return Avatar{}, fmt.Errorf("%w: user id is required", ErrInvalidAvatar)
	case p.UploadID.IsZero():
		return Avatar{}, fmt.Errorf("%w: upload id is required", ErrInvalidAvatar)
	case p.Now.IsZero():
		return Avatar{}, fmt.Errorf("%w: creation time is required", ErrInvalidAvatar)
	}

	return Avatar{
		id:        p.ID,
		version:   1,
		userID:    p.UserID,
		uploadID:  p.UploadID,
		status:    AvatarStatusPending,
		createdAt: p.Now.UTC(),
		updatedAt: p.Now.UTC(),
	}, nil
}

type RestoreAvatarParams struct {
	ID           AvatarID
	Version      int64
	UserID       UserID
	UploadID     UploadRef
	FileID       FileRef
	Status       AvatarStatus
	Snapshot     FileSnapshot
	Variants     []Variant
	FailedReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    time.Time
}

func RestoreAvatar(p RestoreAvatarParams) (Avatar, error) {
	switch {
	case p.ID.IsZero():
		return Avatar{}, fmt.Errorf("%w: id is required", ErrInvalidAvatar)
	case p.Version <= 0:
		return Avatar{}, fmt.Errorf("%w: version must be positive (got %d)",
			ErrInvalidAvatar, p.Version)
	case p.UserID.IsZero():
		return Avatar{}, fmt.Errorf("%w: user id is required", ErrInvalidAvatar)
	case !p.Status.IsValid():
		return Avatar{}, fmt.Errorf("%w: status %q", ErrInvalidAvatar, p.Status.String())
	case len(p.Variants) > maxVariantsPerAvatar:
		return Avatar{}, fmt.Errorf("%w: %d variants exceed the limit of %d",
			ErrInvalidAvatar, len(p.Variants), maxVariantsPerAvatar)
	}

	return Avatar{
		id:           p.ID,
		version:      p.Version,
		userID:       p.UserID,
		uploadID:     p.UploadID,
		fileID:       p.FileID,
		status:       p.Status,
		snapshot:     p.Snapshot,
		variants:     p.Variants,
		failedReason: p.FailedReason,
		createdAt:    p.CreatedAt,
		updatedAt:    p.UpdatedAt,
		deletedAt:    p.DeletedAt,
	}, nil
}

func (a *Avatar) ID() AvatarID { return a.id }

func (a *Avatar) Version() int64 { return a.version }

func (a *Avatar) UserID() UserID { return a.userID }

func (a *Avatar) UploadID() UploadRef { return a.uploadID }

func (a *Avatar) FileID() FileRef { return a.fileID }

func (a *Avatar) Status() AvatarStatus { return a.status }

func (a *Avatar) Snapshot() FileSnapshot { return a.snapshot }

func (a *Avatar) Variants() []Variant { return a.variants }

func (a *Avatar) FailedReason() string { return a.failedReason }

func (a *Avatar) CreatedAt() time.Time { return a.createdAt }

func (a *Avatar) UpdatedAt() time.Time { return a.updatedAt }

func (a *Avatar) DeletedAt() time.Time { return a.deletedAt }

func (a *Avatar) IsDeleted() bool { return !a.deletedAt.IsZero() }

func (a *Avatar) OwnedBy(user UserID) bool { return a.userID.Equal(user) }

func (a *Avatar) IsNewerThan(other Avatar) bool {
	if !a.createdAt.Equal(other.createdAt) {
		return a.createdAt.After(other.createdAt)
	}

	return a.id.Compare(other.id) > 0
}

func (a *Avatar) SetVersion(version int64) error {
	if version <= 0 {
		return fmt.Errorf("%w: version must be positive (got %d)", ErrInvalidAvatar, version)
	}

	a.version = version

	return nil
}

func (a *Avatar) VariantByName(name string) (Variant, bool) {
	for _, variant := range a.variants {
		if variant.Name() == name {
			return variant, true
		}
	}

	return Variant{}, false
}

func (a *Avatar) StartProcessing(fileID FileRef, now time.Time) error {
	if fileID.IsZero() {
		return fmt.Errorf("%w: file id is required to start processing", ErrInvalidAvatar)
	}

	if err := a.prepare(AvatarStatusProcessing, now); err != nil {
		return err
	}

	a.fileID = fileID
	a.apply(AvatarStatusProcessing, now)

	return nil
}

func (a *Avatar) MarkReady(snapshot FileSnapshot, variants []Variant, now time.Time) error {
	switch {
	case snapshot.IsZero():
		return fmt.Errorf("%w: a snapshot is required to become ready", ErrInvalidAvatar)
	case len(variants) == 0:
		return fmt.Errorf("%w: at least one variant is required to become ready", ErrInvalidAvatar)
	case len(variants) > maxVariantsPerAvatar:
		return fmt.Errorf("%w: %d variants exceed the limit of %d",
			ErrInvalidAvatar, len(variants), maxVariantsPerAvatar)
	}

	for _, variant := range variants {
		if variant.IsZero() {
			return fmt.Errorf("%w: a variant is empty", ErrInvalidAvatar)
		}
	}

	if err := a.prepare(AvatarStatusReady, now); err != nil {
		return err
	}

	a.snapshot = snapshot
	a.variants = variants
	a.failedReason = ""
	a.apply(AvatarStatusReady, now)

	return nil
}

func (a *Avatar) MarkFailed(reason string, now time.Time) error {
	if reason == "" {
		return fmt.Errorf("%w: a reason is required to fail", ErrInvalidAvatar)
	}

	if err := a.prepare(AvatarStatusFailed, now); err != nil {
		return err
	}

	a.failedReason = reason
	a.apply(AvatarStatusFailed, now)

	return nil
}

func (a *Avatar) MarkDeleted(now time.Time) error {
	if err := a.prepare(AvatarStatusDeleted, now); err != nil {
		return err
	}

	a.deletedAt = now.UTC()
	a.apply(AvatarStatusDeleted, now)

	return nil
}

func (a *Avatar) prepare(next AvatarStatus, now time.Time) error {
	if now.IsZero() {
		return fmt.Errorf("%w: transition time is required", ErrInvalidAvatar)
	}

	if !a.status.CanBecome(next) {
		return fmt.Errorf("%w: %s -> %s",
			ErrForbiddenAvatarTransition, a.status.String(), next.String())
	}

	return nil
}

func (a *Avatar) apply(next AvatarStatus, now time.Time) {
	a.status = next
	a.updatedAt = now.UTC()
}

func (a *Avatar) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Avatar: serialize via DTO, not directly")
}
