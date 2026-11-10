package domain

import (
	"errors"
	"fmt"
	"math"
)

var ErrInvalidDimensions = errors.New("invalid dimensions")

type Dimensions struct {
	width  int32
	height int32
}

func NewDimensions(width, height int) (Dimensions, error) {
	if err := validateDimensions(width, height); err != nil {
		return Dimensions{}, err
	}

	return Dimensions{width: int32(width), height: int32(height)}, nil
}

func RestoreDimensions(width, height int32) (Dimensions, error) {
	if err := validateDimensions(int(width), int(height)); err != nil {
		return Dimensions{}, err
	}

	return Dimensions{width: width, height: height}, nil
}

func validateDimensions(width, height int) error {
	switch {
	case width <= 0 || height <= 0:
		return fmt.Errorf(
			"%w: both sides must be positive (got %dx%d)",
			ErrInvalidDimensions, width, height,
		)
	case width > math.MaxInt32 || height > math.MaxInt32:
		return fmt.Errorf(
			"%w: side must not exceed %d (got %dx%d)",
			ErrInvalidDimensions, math.MaxInt32, width, height,
		)
	}

	return nil
}

func (d Dimensions) Width() int32 { return d.width }

func (d Dimensions) Height() int32 { return d.height }

func (d Dimensions) Pixels() int64 { return int64(d.width) * int64(d.height) }

func (d Dimensions) String() string {
	if d.IsZero() {
		return ""
	}

	return fmt.Sprintf("%dx%d", d.width, d.height)
}

func (d Dimensions) IsZero() bool { return d.width == 0 && d.height == 0 }

func (d Dimensions) Equal(other Dimensions) bool { return d == other }

func (d Dimensions) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Dimensions: serialize via DTO, not directly")
}
