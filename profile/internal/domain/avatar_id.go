package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalidAvatarID = errors.New("invalid avatar id")

type AvatarID struct {
	value uuid.UUID
}

func NewAvatarID() (AvatarID, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return AvatarID{}, fmt.Errorf("%w: %w", ErrInvalidAvatarID, err)
	}

	return AvatarID{value: value}, nil
}

func ParseAvatarID(raw string) (AvatarID, error) {
	value, err := uuid.Parse(raw)
	if err != nil {
		return AvatarID{}, fmt.Errorf("%w: %w", ErrInvalidAvatarID, err)
	}

	if value.Version() != 7 {
		return AvatarID{}, fmt.Errorf(
			"%w: expected a v7 uuid (got v%d)", ErrInvalidAvatarID, value.Version())
	}

	return AvatarID{value: value}, nil
}

func AvatarIDFromUUID(value uuid.UUID) AvatarID { return AvatarID{value: value} }

func (a AvatarID) UUID() uuid.UUID { return a.value }

func (a AvatarID) String() string { return a.value.String() }

func (a AvatarID) IsZero() bool { return a.value == uuid.Nil }

func (a AvatarID) Equal(other AvatarID) bool { return a.value == other.value }

func (a AvatarID) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.AvatarID: serialize via DTO, not directly")
}
