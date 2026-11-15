package domain

import (
	"errors"
	"fmt"
)

const (
	rejectReasonUnsupportedFormat = "unsupported_format"
	rejectReasonMagicMismatch     = "magic_mismatch"
	rejectReasonDecodeFailed      = "decode_failed"
	rejectReasonTooManyPixels     = "too_many_pixels"
	rejectReasonTooSmall          = "too_small"
	rejectReasonSizeMismatch      = "size_mismatch"
	rejectReasonContainerInvalid  = "container_invalid"
	rejectReasonContainerTooLarge = "container_too_large"
	rejectReasonInternalError     = "internal_error"
)

var (
	RejectReasonUnsupportedFormat = RejectReason{value: rejectReasonUnsupportedFormat}
	RejectReasonMagicMismatch     = RejectReason{value: rejectReasonMagicMismatch}
	RejectReasonDecodeFailed      = RejectReason{value: rejectReasonDecodeFailed}
	RejectReasonTooManyPixels     = RejectReason{value: rejectReasonTooManyPixels}
	RejectReasonTooSmall          = RejectReason{value: rejectReasonTooSmall}
	RejectReasonSizeMismatch      = RejectReason{value: rejectReasonSizeMismatch}
	RejectReasonContainerInvalid  = RejectReason{value: rejectReasonContainerInvalid}
	RejectReasonContainerTooLarge = RejectReason{value: rejectReasonContainerTooLarge}
	RejectReasonInternalError     = RejectReason{value: rejectReasonInternalError}
)

var ErrInvalidRejectReason = errors.New("invalid reject reason")

type RejectReason struct {
	value string
}

func ParseRejectReason(raw string) (RejectReason, error) {
	switch raw {
	case rejectReasonUnsupportedFormat,
		rejectReasonMagicMismatch,
		rejectReasonDecodeFailed,
		rejectReasonTooManyPixels,
		rejectReasonTooSmall,
		rejectReasonSizeMismatch,
		rejectReasonContainerInvalid,
		rejectReasonContainerTooLarge,
		rejectReasonInternalError:
		return RejectReason{value: raw}, nil
	default:
		return RejectReason{}, fmt.Errorf("%w: %q", ErrInvalidRejectReason, raw)
	}
}

func (r RejectReason) String() string { return r.value }

func (r RejectReason) IsZero() bool { return r.value == "" }

func (r RejectReason) Equal(other RejectReason) bool { return r.value == other.value }

func (r RejectReason) IsValid() bool {
	switch r.value {
	case rejectReasonUnsupportedFormat,
		rejectReasonMagicMismatch,
		rejectReasonDecodeFailed,
		rejectReasonTooManyPixels,
		rejectReasonTooSmall,
		rejectReasonSizeMismatch,
		rejectReasonContainerInvalid,
		rejectReasonContainerTooLarge,
		rejectReasonInternalError:
		return true
	default:
		return false
	}
}

func (r RejectReason) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.RejectReason: serialize via DTO, not directly")
}
