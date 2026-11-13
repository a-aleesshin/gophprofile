package domain

import (
	"errors"
	"fmt"
)

const (
	avatarStatusPending    = "pending"
	avatarStatusProcessing = "processing"
	avatarStatusReady      = "ready"
	avatarStatusFailed     = "failed"
	avatarStatusDeleted    = "deleted"
)

var (
	AvatarStatusPending    = AvatarStatus{value: avatarStatusPending}
	AvatarStatusProcessing = AvatarStatus{value: avatarStatusProcessing}
	AvatarStatusReady      = AvatarStatus{value: avatarStatusReady}
	AvatarStatusFailed     = AvatarStatus{value: avatarStatusFailed}
	AvatarStatusDeleted    = AvatarStatus{value: avatarStatusDeleted}
)

var (
	ErrInvalidAvatarStatus       = errors.New("invalid avatar status")
	ErrForbiddenAvatarTransition = errors.New("forbidden avatar transition")
)

var avatarTransitions = map[string][]string{
	avatarStatusPending: {
		avatarStatusProcessing,
		avatarStatusFailed,
		avatarStatusDeleted,
	},
	avatarStatusProcessing: {
		avatarStatusReady,
		avatarStatusFailed,
		avatarStatusDeleted,
	},
	avatarStatusReady:   {avatarStatusDeleted},
	avatarStatusFailed:  {avatarStatusDeleted},
	avatarStatusDeleted: {},
}

type AvatarStatus struct {
	value string
}

func ParseAvatarStatus(raw string) (AvatarStatus, error) {
	status := AvatarStatus{value: raw}

	if !status.IsValid() {
		return AvatarStatus{}, fmt.Errorf("%w: %q", ErrInvalidAvatarStatus, raw)
	}

	return status, nil
}

func (s AvatarStatus) String() string { return s.value }

func (s AvatarStatus) IsZero() bool { return s.value == "" }

func (s AvatarStatus) Equal(other AvatarStatus) bool { return s.value == other.value }

func (s AvatarStatus) IsValid() bool {
	switch s.value {
	case avatarStatusPending, avatarStatusProcessing, avatarStatusReady,
		avatarStatusFailed, avatarStatusDeleted:
		return true
	default:
		return false
	}
}

func (s AvatarStatus) IsTerminal() bool { return s.value == avatarStatusDeleted }

func (s AvatarStatus) IsServable() bool { return s.value == avatarStatusReady }

func (s AvatarStatus) CanBecome(next AvatarStatus) bool {
	allowed, known := avatarTransitions[s.value]
	if !known || !next.IsValid() {
		return false
	}

	for _, candidate := range allowed {
		if candidate == next.value {
			return true
		}
	}

	return false
}

func (s AvatarStatus) MarshalJSON() ([]byte, error) {
	return nil, errors.New("domain.AvatarStatus: serialize via DTO, not directly")
}
