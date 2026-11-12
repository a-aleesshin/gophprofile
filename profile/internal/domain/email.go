package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	maxEmailLength = 254
	emailHashChars = sha256.Size * 2
)

var (
	ErrInvalidEmail     = errors.New("invalid email")
	ErrInvalidEmailHash = errors.New("invalid email hash")
)

type Email struct {
	value string
}

func NewEmail(raw string) (Email, error) {
	value := strings.ToLower(strings.TrimSpace(raw))

	if err := validateEmail(value); err != nil {
		return Email{}, err
	}

	return Email{value: value}, nil
}

func RestoreEmail(raw string) (Email, error) {
	if raw != strings.ToLower(strings.TrimSpace(raw)) {
		return Email{}, fmt.Errorf(
			"%w: email is not canonical (expected lowercase without surrounding whitespace)",
			ErrInvalidEmail)
	}

	if err := validateEmail(raw); err != nil {
		return Email{}, err
	}

	return Email{value: raw}, nil
}

func validateEmail(value string) error {
	switch {
	case value == "":
		return fmt.Errorf("%w: email is required", ErrInvalidEmail)
	case len(value) > maxEmailLength:
		return fmt.Errorf("%w: email exceeds %d bytes (got %d)",
			ErrInvalidEmail, maxEmailLength, len(value))
	}

	local, domain, found := strings.Cut(value, "@")

	switch {
	case !found:
		return fmt.Errorf("%w: email must contain @", ErrInvalidEmail)
	case local == "":
		return fmt.Errorf("%w: email has no local part", ErrInvalidEmail)
	case domain == "":
		return fmt.Errorf("%w: email has no domain", ErrInvalidEmail)
	case strings.Contains(domain, "@"):
		return fmt.Errorf("%w: email has more than one @", ErrInvalidEmail)
	case !strings.Contains(domain, "."):
		return fmt.Errorf("%w: email domain has no dot", ErrInvalidEmail)
	case strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, "."):
		return fmt.Errorf("%w: email domain starts or ends with a dot", ErrInvalidEmail)
	}

	for _, symbol := range value {
		if unicode.IsSpace(symbol) || unicode.IsControl(symbol) {
			return fmt.Errorf("%w: email must not contain whitespace", ErrInvalidEmail)
		}
	}

	return nil
}

func (e Email) String() string { return e.value }

func (e Email) IsZero() bool { return e.value == "" }

func (e Email) Hash() EmailHash {
	digest := sha256.Sum256([]byte(e.value))

	return EmailHash{value: hex.EncodeToString(digest[:])}
}

func (e Email) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Email: serialize via DTO, not directly")
}

type EmailHash struct {
	value string
}

func ParseEmailHash(raw string) (EmailHash, error) {
	value := strings.ToLower(strings.TrimSpace(raw))

	if len(value) != emailHashChars {
		return EmailHash{}, fmt.Errorf("%w: expected %d hex characters (got %d)",
			ErrInvalidEmailHash, emailHashChars, len(value))
	}

	if _, err := hex.DecodeString(value); err != nil {
		return EmailHash{}, fmt.Errorf("%w: %w", ErrInvalidEmailHash, err)
	}

	return EmailHash{value: value}, nil
}

func RestoreEmailHash(raw string) (EmailHash, error) {
	if raw != strings.ToLower(raw) {
		return EmailHash{}, fmt.Errorf("%w: hash must be lowercase hex", ErrInvalidEmailHash)
	}

	return ParseEmailHash(raw)
}

func (h EmailHash) String() string { return h.value }

func (h EmailHash) IsZero() bool { return h.value == "" }

func (h EmailHash) Equal(other EmailHash) bool { return h.value == other.value }

func (h EmailHash) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.EmailHash: serialize via DTO, not directly")
}
