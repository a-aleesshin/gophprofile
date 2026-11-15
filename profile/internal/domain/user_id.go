package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const maxUserIDLength = 255

var ErrInvalidUserID = errors.New("invalid user id")

type UserID struct {
	value string
}

func NewUserID(raw string) (UserID, error) {
	value := strings.TrimSpace(raw)

	if err := validateUserID(value); err != nil {
		return UserID{}, err
	}

	return UserID{value: value}, nil
}

func RestoreUserID(raw string) (UserID, error) {
	if err := validateUserID(raw); err != nil {
		return UserID{}, err
	}

	return UserID{value: raw}, nil
}

func validateUserID(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: user id is required", ErrInvalidUserID)
	case len(value) > maxUserIDLength:
		return fmt.Errorf("%w: user id exceeds %d bytes (got %d)",
			ErrInvalidUserID, maxUserIDLength, len(value))
	}

	for _, symbol := range value {
		if unicode.IsControl(symbol) {
			return fmt.Errorf("%w: user id must not contain control characters", ErrInvalidUserID)
		}
	}

	return nil
}

func (u UserID) String() string { return u.value }

func (u UserID) IsZero() bool { return u.value == "" }

func (u UserID) Equal(other UserID) bool { return u.value == other.value }

func (u UserID) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.UserID: serialize via DTO, not directly")
}
