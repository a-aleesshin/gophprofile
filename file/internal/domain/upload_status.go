package domain

import (
	"errors"
	"fmt"
)

const (
	uploadStatusPending  = "pending"
	uploadStatusUploaded = "uploaded"
	uploadStatusExpired  = "expired"
	uploadStatusAborted  = "aborted"
)

var (
	UploadStatusPending  = UploadStatus{value: uploadStatusPending}
	UploadStatusUploaded = UploadStatus{value: uploadStatusUploaded}
	UploadStatusExpired  = UploadStatus{value: uploadStatusExpired}
	UploadStatusAborted  = UploadStatus{value: uploadStatusAborted}
)

var (
	ErrInvalidUploadStatus       = errors.New("invalid upload status")
	ErrForbiddenUploadTransition = errors.New("forbidden upload status transition")
)

type UploadStatus struct {
	value string
}

func ParseUploadStatus(raw string) (UploadStatus, error) {
	switch raw {
	case uploadStatusPending,
		uploadStatusUploaded,
		uploadStatusExpired,
		uploadStatusAborted:
		return UploadStatus{value: raw}, nil
	default:
		return UploadStatus{}, fmt.Errorf("%w: %q", ErrInvalidUploadStatus, raw)
	}
}

func (s UploadStatus) String() string { return s.value }

func (s UploadStatus) IsZero() bool { return s.value == "" }

func (s UploadStatus) Equal(other UploadStatus) bool { return s.value == other.value }

func (s UploadStatus) IsValid() bool {
	switch s.value {
	case uploadStatusPending,
		uploadStatusUploaded,
		uploadStatusExpired,
		uploadStatusAborted:
		return true
	default:
		return false
	}
}

func (s UploadStatus) IsTerminal() bool {
	switch s.value {
	case uploadStatusUploaded, uploadStatusExpired, uploadStatusAborted:
		return true
	default:
		return false
	}
}

func (s UploadStatus) CanTransitionTo(next UploadStatus) bool {
	if !s.IsValid() || !next.IsValid() {
		return false
	}

	switch s.value {
	case uploadStatusPending:
		return next.value == uploadStatusUploaded ||
			next.value == uploadStatusExpired ||
			next.value == uploadStatusAborted
	default:
		return false
	}
}

func (s UploadStatus) TransitionTo(next UploadStatus) (UploadStatus, error) {
	if !next.IsValid() {
		return s, fmt.Errorf("%w: %q", ErrInvalidUploadStatus, next.value)
	}
	if !s.CanTransitionTo(next) {
		return s, fmt.Errorf("%w: %q -> %q", ErrForbiddenUploadTransition, s.value, next.value)
	}

	return next, nil
}

func (s UploadStatus) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.UploadStatus: serialize via DTO, not directly")
}
