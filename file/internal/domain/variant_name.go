package domain

import (
	"errors"
	"fmt"
	"strings"
)

const maxVariantNameLength = 32

const variantNameOriginal = "original"

var VariantNameOriginal = VariantName{value: variantNameOriginal}

var ErrInvalidVariantName = errors.New("invalid variant name")

type VariantName struct {
	value string
}

func NewVariantName(raw string) (VariantName, error) {
	value := strings.ToLower(strings.TrimSpace(raw))

	if err := validateVariantName(value); err != nil {
		return VariantName{}, err
	}

	return VariantName{value: value}, nil
}

func RestoreVariantName(raw string) (VariantName, error) {
	if raw != strings.ToLower(strings.TrimSpace(raw)) {
		return VariantName{}, fmt.Errorf(
			"%w: name is not canonical (expected lowercase without surrounding whitespace)",
			ErrInvalidVariantName,
		)
	}

	if err := validateVariantName(raw); err != nil {
		return VariantName{}, err
	}

	return VariantName{value: raw}, nil
}

func validateVariantName(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: name is required", ErrInvalidVariantName)
	case len(value) > maxVariantNameLength:
		return fmt.Errorf(
			"%w: name exceeds %d bytes (got %d)",
			ErrInvalidVariantName, maxVariantNameLength, len(value),
		)
	case value[0] == '-':
		return fmt.Errorf("%w: name must not start with a hyphen", ErrInvalidVariantName)
	case value[len(value)-1] == '-':
		return fmt.Errorf("%w: name must not end with a hyphen", ErrInvalidVariantName)
	}

	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf(
				"%w: name must contain only [a-z0-9-] (got %q)",
				ErrInvalidVariantName, value[i:i+1],
			)
		}
	}

	return nil
}

func (n VariantName) String() string { return n.value }

func (n VariantName) IsZero() bool { return n.value == "" }

func (n VariantName) Equal(other VariantName) bool { return n.value == other.value }

func (n VariantName) IsOriginal() bool { return n.value == variantNameOriginal }

func (n VariantName) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.VariantName: serialize via DTO, not directly")
}
