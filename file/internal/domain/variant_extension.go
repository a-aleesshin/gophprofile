package domain

import (
	"errors"
	"fmt"
	"strings"
)

const maxExtensionLength = 8

var ErrInvalidExtension = errors.New("invalid extension")

type Extension struct {
	value string
}

func NewExtension(raw string) (Extension, error) {
	value := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), ".")))

	if err := validateExtension(value); err != nil {
		return Extension{}, err
	}

	return Extension{value: value}, nil
}

func RestoreExtension(raw string) (Extension, error) {
	if raw != strings.ToLower(strings.TrimSpace(raw)) {
		return Extension{}, fmt.Errorf(
			"%w: extension is not canonical (expected lowercase without surrounding whitespace)",
			ErrInvalidExtension,
		)
	}

	if err := validateExtension(raw); err != nil {
		return Extension{}, err
	}

	return Extension{value: raw}, nil
}

func validateExtension(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: extension is required", ErrInvalidExtension)
	case len(value) > maxExtensionLength:
		return fmt.Errorf(
			"%w: extension exceeds %d bytes (got %d)",
			ErrInvalidExtension, maxExtensionLength, len(value),
		)
	}

	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return fmt.Errorf(
				"%w: extension must contain only [a-z0-9] (got %q)",
				ErrInvalidExtension, value[i:i+1],
			)
		}
	}

	return nil
}

func (e Extension) String() string { return e.value }

func (e Extension) IsZero() bool { return e.value == "" }

func (e Extension) Equal(other Extension) bool { return e.value == other.value }

func (e Extension) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Extension: serialize via DTO, not directly")
}
