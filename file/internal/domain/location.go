package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxLocationLength = 512

const (
	stagingPrefix   = "staging"
	originalsPrefix = "originals"
	variantsPrefix  = "variants"
)

var ErrInvalidLocation = errors.New("invalid location")

type Location struct {
	value string
}

func NewStagingLocation(id UploadID) (Location, error) {
	if id.IsZero() {
		return Location{}, fmt.Errorf("%w: upload id is required", ErrInvalidLocation)
	}

	return newLocation(stagingPrefix + "/" + id.String())
}

func NewOriginalLocation(id FileID) (Location, error) {
	if id.IsZero() {
		return Location{}, fmt.Errorf("%w: file id is required", ErrInvalidLocation)
	}

	return newLocation(originalsPrefix + "/" + id.String())
}

func NewVariantLocation(id FileID, name VariantName, ext Extension) (Location, error) {
	switch {
	case id.IsZero():
		return Location{}, fmt.Errorf("%w: file id is required", ErrInvalidLocation)
	case name.IsZero():
		return Location{}, fmt.Errorf("%w: variant name is required", ErrInvalidLocation)
	case ext.IsZero():
		return Location{}, fmt.Errorf("%w: extension is required", ErrInvalidLocation)
	}

	return newLocation(variantsPrefix + "/" + id.String() + "/" + name.String() + "." + ext.String())
}

func RestoreLocation(raw string) (Location, error) {
	return newLocation(raw)
}

func newLocation(value string) (Location, error) {
	if err := validateLocation(value); err != nil {
		return Location{}, err
	}

	return Location{value: value}, nil
}

func validateLocation(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: location is required", ErrInvalidLocation)
	case len(value) > maxLocationLength:
		return fmt.Errorf(
			"%w: location exceeds %d bytes (got %d)",
			ErrInvalidLocation, maxLocationLength, len(value),
		)
	case !utf8.ValidString(value):
		return fmt.Errorf("%w: location is not valid UTF-8", ErrInvalidLocation)
	case strings.HasPrefix(value, "/"):
		return fmt.Errorf("%w: location must not start with '/'", ErrInvalidLocation)
	case strings.HasSuffix(value, "/"):
		return fmt.Errorf("%w: location must not end with '/'", ErrInvalidLocation)
	case strings.Contains(value, "//"):
		return fmt.Errorf("%w: location must not contain an empty segment ('//')", ErrInvalidLocation)
	case strings.Contains(value, `\`):
		return fmt.Errorf("%w: location must not contain '\\'", ErrInvalidLocation)
	}

	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%w: segment %q is not allowed", ErrInvalidLocation, segment)
		}
	}

	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: control character %q is not allowed", ErrInvalidLocation, r)
		}
	}

	return nil
}

func (l Location) String() string { return l.value }

func (l Location) IsZero() bool { return l.value == "" }

func (l Location) Equal(other Location) bool { return l.value == other.value }

func (l Location) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Location: serialize via DTO, not directly")
}
