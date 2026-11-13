package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalidReference = errors.New("invalid reference to the file service")

type FileRef struct {
	value uuid.UUID
}

func ParseFileRef(raw string) (FileRef, error) {
	value, err := uuid.Parse(raw)
	if err != nil {
		return FileRef{}, fmt.Errorf("%w: file id %q: %w", ErrInvalidReference, raw, err)
	}

	if value == uuid.Nil {
		return FileRef{}, fmt.Errorf("%w: file id must not be nil", ErrInvalidReference)
	}

	return FileRef{value: value}, nil
}

func FileRefFromUUID(value uuid.UUID) FileRef { return FileRef{value: value} }

func (f FileRef) UUID() uuid.UUID { return f.value }

func (f FileRef) String() string { return f.value.String() }

func (f FileRef) IsZero() bool { return f.value == uuid.Nil }

func (f FileRef) Equal(other FileRef) bool { return f.value == other.value }

func (f FileRef) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.FileRef: serialize via DTO, not directly")
}

type UploadRef struct {
	value uuid.UUID
}

func ParseUploadRef(raw string) (UploadRef, error) {
	value, err := uuid.Parse(raw)
	if err != nil {
		return UploadRef{}, fmt.Errorf("%w: upload id %q: %w", ErrInvalidReference, raw, err)
	}

	if value == uuid.Nil {
		return UploadRef{}, fmt.Errorf("%w: upload id must not be nil", ErrInvalidReference)
	}

	return UploadRef{value: value}, nil
}

func UploadRefFromUUID(value uuid.UUID) UploadRef { return UploadRef{value: value} }

func (u UploadRef) UUID() uuid.UUID { return u.value }

func (u UploadRef) String() string { return u.value.String() }

func (u UploadRef) IsZero() bool { return u.value == uuid.Nil }

func (u UploadRef) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.UploadRef: serialize via DTO, not directly")
}
