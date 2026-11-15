package domain

import (
	"errors"
	"fmt"
	"mime"
	"strings"
)

const maxMimeTypeLength = 255

var ErrInvalidMimeType = errors.New("invalid mime type")

type MimeType struct {
	value string
}

func NewMimeType(raw string) (MimeType, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return MimeType{}, fmt.Errorf("%w: %w", ErrInvalidMimeType, err)
	}

	if err := validateMimeType(mediaType); err != nil {
		return MimeType{}, err
	}

	return MimeType{value: mediaType}, nil
}

func RestoreMimeType(raw string) (MimeType, error) {
	if err := validateMimeType(raw); err != nil {
		return MimeType{}, err
	}

	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return MimeType{}, fmt.Errorf("%w: %w", ErrInvalidMimeType, err)
	}
	if mediaType != raw || len(params) > 0 {
		return MimeType{}, fmt.Errorf(
			"%w: mime type is not canonical (expected lowercase %q without parameters)",
			ErrInvalidMimeType, mediaType,
		)
	}

	return MimeType{value: raw}, nil
}

func validateMimeType(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: mime type is required", ErrInvalidMimeType)
	case len(value) > maxMimeTypeLength:
		return fmt.Errorf(
			"%w: mime type exceeds %d bytes (got %d)",
			ErrInvalidMimeType, maxMimeTypeLength, len(value),
		)
	}

	essence, subtype, found := strings.Cut(value, "/")
	if !found {
		return fmt.Errorf("%w: expected type/subtype (got %q)", ErrInvalidMimeType, value)
	}
	if err := validateMimeToken(essence); err != nil {
		return err
	}

	return validateMimeToken(subtype)
}

func validateMimeToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: type and subtype must not be empty", ErrInvalidMimeType)
	}

	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			strings.IndexByte("!#$&-^_.+", c) >= 0:
		default:
			return fmt.Errorf(
				"%w: %q is not allowed in a mime token (got %q)",
				ErrInvalidMimeType, token[i:i+1], token,
			)
		}
	}

	return nil
}

func (m MimeType) String() string { return m.value }

func (m MimeType) IsZero() bool { return m.value == "" }

func (m MimeType) Equal(other MimeType) bool { return m.value == other.value }

func (m MimeType) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.MimeType: serialize via DTO, not directly")
}
