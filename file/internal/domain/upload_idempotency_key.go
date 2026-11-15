package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxIdempotencyKeyLength = 255

var ErrInvalidIdempotencyKey = errors.New("invalid idempotency key")

type IdempotencyKey struct {
	value string
}

func NewIdempotencyKey(raw string) (IdempotencyKey, error) {
	value := strings.TrimSpace(raw)

	if err := validateIdempotencyKey(value); err != nil {
		return IdempotencyKey{}, err
	}

	return IdempotencyKey{value: value}, nil
}

func RestoreIdempotencyKey(raw string) (IdempotencyKey, error) {
	if raw != strings.TrimSpace(raw) {
		return IdempotencyKey{}, fmt.Errorf(
			"%w: key is not canonical (expected no surrounding whitespace)",
			ErrInvalidIdempotencyKey,
		)
	}

	if err := validateIdempotencyKey(raw); err != nil {
		return IdempotencyKey{}, err
	}

	return IdempotencyKey{value: raw}, nil
}

func validateIdempotencyKey(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: key is required", ErrInvalidIdempotencyKey)
	case len(value) > maxIdempotencyKeyLength:
		return fmt.Errorf(
			"%w: key exceeds %d bytes (got %d)",
			ErrInvalidIdempotencyKey, maxIdempotencyKeyLength, len(value),
		)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: key is not valid UTF-8", ErrInvalidIdempotencyKey)
	}

	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf(
				"%w: control character %q is not allowed",
				ErrInvalidIdempotencyKey, r,
			)
		}
	}

	return nil
}

func (k IdempotencyKey) String() string { return k.value }

func (k IdempotencyKey) IsZero() bool { return k.value == "" }

func (k IdempotencyKey) Equal(other IdempotencyKey) bool { return k.value == other.value }

func (k IdempotencyKey) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.IdempotencyKey: serialize via DTO, not directly")
}
