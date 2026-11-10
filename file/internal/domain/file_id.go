package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	defaultVersion uuid.Version = 7
)

var (
	ErrFailedToGenerateFileID = errors.New("failed to generate FileID")
	ErrEmptyFileID            = errors.New("empty FileID")
	ErrInvalidFileIDVersion   = errors.New("invalid FileID version")
	ErrInvalidFileID          = errors.New("invalid FileID")
)

type FileID struct {
	value uuid.UUID
}

func NewFileID() (FileID, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return FileID{}, fmt.Errorf("%w: %w", ErrFailedToGenerateFileID, err)
	}

	return FileID{value: value}, nil
}

func ParseFileID(value string) (FileID, error) {
	if value == "" {
		return FileID{}, ErrEmptyFileID
	}

	uuidValue, err := uuid.Parse(value)
	if err != nil {
		return FileID{}, fmt.Errorf("%w: %w", ErrInvalidFileID, err)
	}
	if uuidValue == uuid.Nil {
		return FileID{}, ErrEmptyFileID
	}
	if uuidValue.Version() != defaultVersion {
		return FileID{}, fmt.Errorf("%w: got v%d", ErrInvalidFileIDVersion, uuidValue.Version())
	}

	return FileID{value: uuidValue}, nil
}

func FileIDFromUUID(v uuid.UUID) FileID {
	return FileID{value: v}
}

func (f FileID) UUID() uuid.UUID {
	return f.value
}

func (f FileID) String() string {
	if f.IsZero() {
		return ""
	}

	return f.value.String()
}

func (f FileID) IsZero() bool {
	return f.value == uuid.Nil
}

func (f FileID) Equal(other FileID) bool {
	return f.value == other.value
}

func (f FileID) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.FileID: serialize via DTO, not directly")
}
