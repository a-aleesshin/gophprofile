package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidFile          = errors.New("invalid file")
	ErrFileNotFound         = errors.New("file not found")
	ErrFileAlreadyExists    = errors.New("file already exists")
	ErrVariantAbsent        = errors.New("variant not found")
	ErrVariantAlreadyExists = errors.New("variant already exists")
)

type File struct {
	id      FileID
	version int64

	namespace    Namespace
	ownerRef     OwnerRef
	originalName OriginalName
	mimeType     MimeType
	size         Size
	checksum     Checksum
	dimensions   Dimensions
	location     Location
	status       FileStatus
	rejectReason RejectReason
	createdAt    time.Time
	updatedAt    time.Time
	deletedAt    *time.Time
}

type NewFileParams struct {
	ID           FileID
	Namespace    Namespace
	OwnerRef     OwnerRef
	OriginalName OriginalName
	MimeType     MimeType
	Size         Size
	Now          time.Time
}

func NewFile(p NewFileParams) (File, error) {
	switch {
	case p.ID.IsZero():
		return File{}, fmt.Errorf("%w: id is required", ErrInvalidFile)
	case p.Namespace.IsZero():
		return File{}, fmt.Errorf("%w: namespace is required", ErrInvalidFile)
	case p.OwnerRef.IsZero():
		return File{}, fmt.Errorf("%w: owner ref is required", ErrInvalidFile)
	case p.OriginalName.IsZero():
		return File{}, fmt.Errorf("%w: original name is required", ErrInvalidFile)
	case p.MimeType.IsZero():
		return File{}, fmt.Errorf("%w: mime type is required", ErrInvalidFile)
	case p.Size.IsZero():
		return File{}, fmt.Errorf("%w: size is required", ErrInvalidFile)
	case p.Now.IsZero():
		return File{}, fmt.Errorf("%w: now is required", ErrInvalidFile)
	}

	location, err := NewOriginalLocation(p.ID)
	if err != nil {
		return File{}, err
	}

	now := p.Now.UTC()

	return File{
		id:           p.ID,
		version:      1,
		namespace:    p.Namespace,
		ownerRef:     p.OwnerRef,
		originalName: p.OriginalName,
		mimeType:     p.MimeType,
		size:         p.Size,
		location:     location,
		status:       FileStatusReceived,
		createdAt:    now,
		updatedAt:    now,
	}, nil
}

type RestoreFileParams struct {
	ID           FileID
	Version      int64
	Namespace    Namespace
	OwnerRef     OwnerRef
	OriginalName OriginalName
	MimeType     MimeType
	Size         Size
	Checksum     Checksum
	Dimensions   Dimensions
	Location     Location
	Status       FileStatus
	RejectReason RejectReason
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

func RestoreFile(p RestoreFileParams) (File, error) {
	switch {
	case p.ID.IsZero():
		return File{}, fmt.Errorf("%w: id is required", ErrInvalidFile)
	case p.Version < 1:
		return File{}, fmt.Errorf("%w: version must be positive (got %d)", ErrInvalidFile, p.Version)
	case p.Namespace.IsZero():
		return File{}, fmt.Errorf("%w: namespace is required", ErrInvalidFile)
	case p.OwnerRef.IsZero():
		return File{}, fmt.Errorf("%w: owner ref is required", ErrInvalidFile)
	case p.Location.IsZero():
		return File{}, fmt.Errorf("%w: location is required", ErrInvalidFile)
	case !p.Status.IsValid():
		return File{}, fmt.Errorf("%w: status %q is not valid", ErrInvalidFile, p.Status.String())
	case p.CreatedAt.IsZero():
		return File{}, fmt.Errorf("%w: created at is required", ErrInvalidFile)
	case p.UpdatedAt.IsZero():
		return File{}, fmt.Errorf("%w: updated at is required", ErrInvalidFile)
	}

	f := File{
		id:           p.ID,
		version:      p.Version,
		namespace:    p.Namespace,
		ownerRef:     p.OwnerRef,
		originalName: p.OriginalName,
		mimeType:     p.MimeType,
		size:         p.Size,
		checksum:     p.Checksum,
		dimensions:   p.Dimensions,
		location:     p.Location,
		status:       p.Status,
		rejectReason: p.RejectReason,
		createdAt:    p.CreatedAt.UTC(),
		updatedAt:    p.UpdatedAt.UTC(),
	}

	if p.DeletedAt != nil {
		deletedAt := p.DeletedAt.UTC()
		f.deletedAt = &deletedAt
	}

	return f, nil
}

func (f *File) StartProcessing(now time.Time) error {
	next, err := f.prepare(FileStatusProcessing, now)
	if err != nil {
		return err
	}

	f.apply(next, now)

	return nil
}

func (f *File) MarkReady(checksum Checksum, dimensions Dimensions, now time.Time) error {
	next, err := f.prepare(FileStatusReady, now)
	if err != nil {
		return err
	}
	if checksum.IsZero() {
		return fmt.Errorf("%w: checksum is required to become ready", ErrInvalidFile)
	}

	f.checksum = checksum
	f.dimensions = dimensions
	f.apply(next, now)

	return nil
}

func (f *File) Reject(reason RejectReason, now time.Time) error {
	next, err := f.prepare(FileStatusRejected, now)
	if err != nil {
		return err
	}
	if !reason.IsValid() {
		return fmt.Errorf("%w: a valid reject reason is required", ErrInvalidFile)
	}

	f.rejectReason = reason
	f.apply(next, now)

	return nil
}

func (f *File) MarkForDeletion(now time.Time) error {
	next, err := f.prepare(FileStatusDeleting, now)
	if err != nil {
		return err
	}

	deletedAt := now.UTC()
	f.deletedAt = &deletedAt
	f.apply(next, now)

	return nil
}

func (f *File) MarkDeleted(now time.Time) error {
	next, err := f.prepare(FileStatusDeleted, now)
	if err != nil {
		return err
	}

	f.apply(next, now)

	return nil
}

func (f *File) SetVersion(version int64) error {
	if version <= f.version {
		return fmt.Errorf(
			"%w: version must grow (have %d, got %d)",
			ErrInvalidFile, f.version, version,
		)
	}

	f.version = version

	return nil
}

func (f *File) prepare(next FileStatus, now time.Time) (FileStatus, error) {
	if now.IsZero() {
		return FileStatus{}, fmt.Errorf("%w: now is required", ErrInvalidFile)
	}

	return f.status.TransitionTo(next)
}

func (f *File) apply(next FileStatus, now time.Time) {
	f.status = next
	f.updatedAt = now.UTC()
}

func (f *File) ID() FileID { return f.id }

func (f *File) Version() int64 { return f.version }

func (f *File) Namespace() Namespace { return f.namespace }

func (f *File) OwnerRef() OwnerRef { return f.ownerRef }

func (f *File) OriginalName() OriginalName { return f.originalName }

func (f *File) MimeType() MimeType { return f.mimeType }

func (f *File) Size() Size { return f.size }

func (f *File) Checksum() Checksum { return f.checksum }

func (f *File) Dimensions() Dimensions { return f.dimensions }

func (f *File) Location() Location { return f.location }

func (f *File) Status() FileStatus { return f.status }

func (f *File) RejectReason() RejectReason { return f.rejectReason }

func (f *File) CreatedAt() time.Time { return f.createdAt }

func (f *File) UpdatedAt() time.Time { return f.updatedAt }

func (f *File) DeletedAt() (time.Time, bool) {
	if f.deletedAt == nil {
		return time.Time{}, false
	}

	return *f.deletedAt, true
}

func (f *File) IsDeleted() bool { return f.deletedAt != nil }

func (f *File) IsOwnedBy(owner OwnerRef) bool { return f.ownerRef.Equal(owner) }

func (f *File) IsServable() bool { return f.status.IsServable() && !f.IsDeleted() }

func (f *File) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.File: serialize via DTO, not directly")
}
