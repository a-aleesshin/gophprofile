package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxOwnerRefLength = 255

var ErrInvalidOwnerRef = errors.New("invalid owner ref")

type OwnerRef struct {
	value string
}

func NewOwnerRef(raw string) (OwnerRef, error) {
	value := strings.TrimSpace(raw)

	if err := validateOwnerRef(value); err != nil {
		return OwnerRef{}, err
	}

	return OwnerRef{value: value}, nil
}

func RestoreOwnerRef(raw string) (OwnerRef, error) {
	if raw != strings.TrimSpace(raw) {
		return OwnerRef{}, fmt.Errorf(
			"%w: owner ref is not canonical (expected no surrounding whitespace)",
			ErrInvalidOwnerRef,
		)
	}

	if err := validateOwnerRef(raw); err != nil {
		return OwnerRef{}, err
	}

	return OwnerRef{value: raw}, nil
}

func validateOwnerRef(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: owner ref is required", ErrInvalidOwnerRef)
	case len(value) > maxOwnerRefLength:
		return fmt.Errorf(
			"%w: owner ref exceeds %d bytes (got %d)",
			ErrInvalidOwnerRef, maxOwnerRefLength, len(value),
		)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: owner ref is not valid UTF-8", ErrInvalidOwnerRef)
	}

	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: control character %q is not allowed", ErrInvalidOwnerRef, r)
		}
	}

	return nil
}

func (o OwnerRef) String() string { return o.value }

func (o OwnerRef) IsZero() bool { return o.value == "" }

func (o OwnerRef) Equal(other OwnerRef) bool { return o.value == other.value }

func (o OwnerRef) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.OwnerRef: serialize via DTO, not directly")
}
