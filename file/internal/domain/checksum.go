package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrInvalidChecksum = errors.New("invalid checksum")

type Checksum struct {
	value [sha256.Size]byte
}

func NewChecksum(sum []byte) (Checksum, error) {
	if len(sum) != sha256.Size {
		return Checksum{}, fmt.Errorf(
			"%w: expected %d bytes (got %d)",
			ErrInvalidChecksum, sha256.Size, len(sum),
		)
	}

	var value [sha256.Size]byte
	copy(value[:], sum)

	if value == ([sha256.Size]byte{}) {
		return Checksum{}, fmt.Errorf("%w: digest must not be all zeroes", ErrInvalidChecksum)
	}

	return Checksum{value: value}, nil
}

func RestoreChecksum(raw string) (Checksum, error) {
	switch {
	case raw == "":
		return Checksum{}, fmt.Errorf("%w: checksum is required", ErrInvalidChecksum)
	case len(raw) != hex.EncodedLen(sha256.Size):
		return Checksum{}, fmt.Errorf(
			"%w: expected %d hex characters (got %d)",
			ErrInvalidChecksum, hex.EncodedLen(sha256.Size), len(raw),
		)
	case raw != strings.ToLower(raw):
		return Checksum{}, fmt.Errorf("%w: checksum must be lowercase hex", ErrInvalidChecksum)
	}

	sum, err := hex.DecodeString(raw)
	if err != nil {
		return Checksum{}, fmt.Errorf("%w: %w", ErrInvalidChecksum, err)
	}

	return NewChecksum(sum)
}

func (c Checksum) String() string {
	if c.IsZero() {
		return ""
	}

	return hex.EncodeToString(c.value[:])
}

func (c Checksum) Bytes() [sha256.Size]byte { return c.value }

func (c Checksum) IsZero() bool { return c.value == ([sha256.Size]byte{}) }

func (c Checksum) Equal(other Checksum) bool { return c.value == other.value }

func (c Checksum) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.Checksum: serialize via DTO, not directly")
}
