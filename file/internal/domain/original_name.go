package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNameLength = 255

var ErrInvalidOriginalName = errors.New("invalid original name")

type OriginalName struct {
	value string
}

func NewOriginalName(raw string) (OriginalName, error) {
	value := strings.TrimSpace(raw)

	if err := validateOriginalName(value); err != nil {
		return OriginalName{}, err
	}

	return OriginalName{value: value}, nil
}

func RestoreOriginalName(raw string) (OriginalName, error) {
	if raw != strings.TrimSpace(raw) {
		return OriginalName{}, fmt.Errorf(
			"%w: name is not canonical (expected no leading or trailing whitespace)",
			ErrInvalidOriginalName,
		)
	}

	if err := validateOriginalName(raw); err != nil {
		return OriginalName{}, err
	}

	return OriginalName{value: raw}, nil
}

func validateOriginalName(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: name is required", ErrInvalidOriginalName)
	case len(value) > maxNameLength:
		return fmt.Errorf(
			"%w: name exceeds %d bytes (got %d)",
			ErrInvalidOriginalName, maxNameLength, len(value),
		)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidOriginalName)
	case value == "." || value == "..":
		return fmt.Errorf("%w: name must not be %q", ErrInvalidOriginalName, value)
	case strings.ContainsAny(value, `/\`):
		return fmt.Errorf(
			"%w: name must not contain path separators ('/' or '\\')",
			ErrInvalidOriginalName,
		)
	}

	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: control character %q is not allowed", ErrInvalidOriginalName, r)
		}
	}

	return nil
}

func (n OriginalName) String() string { return n.value }

func (n OriginalName) IsZero() bool { return n.value == "" }

func (n OriginalName) Equal(other OriginalName) bool { return n.value == other.value }

func (n OriginalName) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.OriginalName: serialize via DTO, not directly")
}
