package domain

import (
	"errors"
	"fmt"
)

const (
	fileStatusReceived   = "received"
	fileStatusProcessing = "processing"
	fileStatusReady      = "ready"
	fileStatusRejected   = "rejected"
	fileStatusDeleting   = "deleting"
	fileStatusDeleted    = "deleted"
)

var (
	FileStatusReceived   = FileStatus{value: fileStatusReceived}
	FileStatusProcessing = FileStatus{value: fileStatusProcessing}
	FileStatusReady      = FileStatus{value: fileStatusReady}
	FileStatusRejected   = FileStatus{value: fileStatusRejected}
	FileStatusDeleting   = FileStatus{value: fileStatusDeleting}
	FileStatusDeleted    = FileStatus{value: fileStatusDeleted}
)

var (
	ErrInvalidFileStatus       = errors.New("invalid file status")
	ErrForbiddenFileTransition = errors.New("forbidden file status transition")
)

type FileStatus struct {
	value string
}

func ParseFileStatus(raw string) (FileStatus, error) {
	switch raw {
	case fileStatusReceived,
		fileStatusProcessing,
		fileStatusReady,
		fileStatusRejected,
		fileStatusDeleting,
		fileStatusDeleted:
		return FileStatus{value: raw}, nil
	default:
		return FileStatus{}, fmt.Errorf("%w: %q", ErrInvalidFileStatus, raw)
	}
}

func (s FileStatus) String() string { return s.value }

func (s FileStatus) IsZero() bool { return s.value == "" }

func (s FileStatus) Equal(other FileStatus) bool { return s.value == other.value }

func (s FileStatus) IsValid() bool {
	switch s.value {
	case fileStatusReceived,
		fileStatusProcessing,
		fileStatusReady,
		fileStatusRejected,
		fileStatusDeleting,
		fileStatusDeleted:
		return true
	default:
		return false
	}
}

func (s FileStatus) IsTerminal() bool { return s.value == fileStatusDeleted }

func (s FileStatus) IsServable() bool { return s.value == fileStatusReady }

func (s FileStatus) CanTransitionTo(next FileStatus) bool {
	if !s.IsValid() || !next.IsValid() {
		return false
	}

	switch s.value {
	case fileStatusReceived:
		return next.value == fileStatusProcessing ||
			next.value == fileStatusDeleting
	case fileStatusProcessing:
		return next.value == fileStatusReady ||
			next.value == fileStatusRejected ||
			next.value == fileStatusDeleting
	case fileStatusReady, fileStatusRejected:
		return next.value == fileStatusDeleting
	case fileStatusDeleting:
		return next.value == fileStatusDeleted
	default:
		return false
	}
}

func (s FileStatus) TransitionTo(next FileStatus) (FileStatus, error) {
	if !next.IsValid() {
		return s, fmt.Errorf("%w: %q", ErrInvalidFileStatus, next.value)
	}
	if !s.CanTransitionTo(next) {
		return s, fmt.Errorf("%w: %q -> %q", ErrForbiddenFileTransition, s.value, next.value)
	}

	return next, nil
}

func (s FileStatus) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.FileStatus: serialize via DTO, not directly")
}
