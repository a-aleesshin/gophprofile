package domain

import (
	"errors"
	"fmt"
	"strings"
)

const maxNamespaceLength = 64

var ErrInvalidNamespace = errors.New("invalid namespace")

type Namespace struct {
	value string
}

func NewNamespace(raw string) (Namespace, error) {
	value := strings.ToLower(strings.TrimSpace(raw))

	if err := validateNamespace(value); err != nil {
		return Namespace{}, err
	}

	return Namespace{value: value}, nil
}

func RestoreNamespace(raw string) (Namespace, error) {
	if raw != strings.ToLower(strings.TrimSpace(raw)) {
		return Namespace{}, fmt.Errorf(
			"%w: namespace is not canonical (expected lowercase without surrounding whitespace)",
			ErrInvalidNamespace,
		)
	}

	if err := validateNamespace(raw); err != nil {
		return Namespace{}, err
	}

	return Namespace{value: raw}, nil
}

func validateNamespace(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: namespace is required", ErrInvalidNamespace)
	case len(value) > maxNamespaceLength:
		return fmt.Errorf(
			"%w: namespace exceeds %d bytes (got %d)",
			ErrInvalidNamespace, maxNamespaceLength, len(value),
		)
	case value[0] < 'a' || value[0] > 'z':
		return fmt.Errorf("%w: namespace must start with a lowercase letter", ErrInvalidNamespace)
	}

	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf(
				"%w: namespace must contain only [a-z0-9-] (got %q)",
				ErrInvalidNamespace, value[i:i+1],
			)
		}
	}

	return nil
}

func (n Namespace) String() string { return n.value }

func (n Namespace) IsZero() bool { return n.value == "" }

func (n Namespace) Equal(other Namespace) bool { return n.value == other.value }

func (n Namespace) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Namespace: serialize via DTO, not directly")
}
