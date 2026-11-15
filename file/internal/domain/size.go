package domain

import (
	"errors"
	"fmt"
)

var ErrInvalidSize = errors.New("invalid size")

type Size struct {
	value int64
}

func NewSize(bytes int64) (Size, error) {
	if bytes <= 0 {
		return Size{}, fmt.Errorf("%w: size must be positive (got %d)", ErrInvalidSize, bytes)
	}

	return Size{value: bytes}, nil
}

func (s Size) Bytes() int64 { return s.value }

func (s Size) IsZero() bool { return s.value == 0 }

func (s Size) Equal(other Size) bool { return s.value == other.value }

func (s Size) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Size: serialize via DTO, not directly")
}
