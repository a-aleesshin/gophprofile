package domain

import (
	"errors"
	"fmt"
)

var ErrInvalidMimeSet = errors.New("invalid mime set")

type MimeSet struct {
	values []MimeType
}

func NewMimeSet(values ...MimeType) (MimeSet, error) {
	if len(values) == 0 {
		return MimeSet{}, fmt.Errorf("%w: at least one mime type is required", ErrInvalidMimeSet)
	}

	unique := make([]MimeType, 0, len(values))

	for _, value := range values {
		if value.IsZero() {
			return MimeSet{}, fmt.Errorf("%w: mime type is required", ErrInvalidMimeSet)
		}

		duplicate := false

		for _, kept := range unique {
			if kept.Equal(value) {
				duplicate = true

				break
			}
		}

		if duplicate {
			return MimeSet{}, fmt.Errorf("%w: %q is listed twice", ErrInvalidMimeSet, value.String())
		}

		unique = append(unique, value)
	}

	return MimeSet{values: unique}, nil
}

func (s MimeSet) Contains(value MimeType) bool {
	if value.IsZero() {
		return false
	}

	for _, kept := range s.values {
		if kept.Equal(value) {
			return true
		}
	}

	return false
}

func (s MimeSet) Len() int { return len(s.values) }

func (s MimeSet) IsZero() bool { return len(s.values) == 0 }

func (s MimeSet) String() string {
	names := make([]byte, 0, len(s.values)*16)

	for i, value := range s.values {
		if i > 0 {
			names = append(names, ", "...)
		}

		names = append(names, value.String()...)
	}

	return string(names)
}

func (s MimeSet) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.MimeSet: serialize via DTO, not directly")
}
