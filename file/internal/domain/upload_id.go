package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrFailedToGenerateUploadID = errors.New("failed to generate UploadID")
	ErrEmptyUploadID            = errors.New("empty UploadID")
	ErrInvalidUploadIDVersion   = errors.New("invalid UploadID version")
	ErrInvalidUploadID          = errors.New("invalid UploadID")
)

type UploadID struct {
	value uuid.UUID
}

func NewUploadID() (UploadID, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return UploadID{}, fmt.Errorf("%w: %w", ErrFailedToGenerateUploadID, err)
	}

	return UploadID{value: value}, nil
}

func ParseUploadID(value string) (UploadID, error) {
	if value == "" {
		return UploadID{}, ErrEmptyUploadID
	}

	uuidValue, err := uuid.Parse(value)
	if err != nil {
		return UploadID{}, fmt.Errorf("%w: %w", ErrInvalidUploadID, err)
	}
	if uuidValue == uuid.Nil {
		return UploadID{}, ErrEmptyUploadID
	}
	if uuidValue.Version() != defaultVersion {
		return UploadID{}, fmt.Errorf("%w: got v%d", ErrInvalidUploadIDVersion, uuidValue.Version())
	}

	return UploadID{value: uuidValue}, nil
}

func UploadIDFromUUID(v uuid.UUID) UploadID {
	return UploadID{value: v}
}

func (u UploadID) UUID() uuid.UUID { return u.value }

func (u UploadID) String() string {
	if u.IsZero() {
		return ""
	}

	return u.value.String()
}

func (u UploadID) IsZero() bool { return u.value == uuid.Nil }

func (u UploadID) Equal(other UploadID) bool { return u.value == other.value }

func (u UploadID) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.UploadID: serialize via DTO, not directly")
}
