package usecase

import (
	"time"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

type AvatarOutcome struct {
	Status domain.AvatarStatus
	Took   time.Duration
}

func outcomeOf(avatar *domain.Avatar, now time.Time) AvatarOutcome {
	return AvatarOutcome{
		Status: avatar.Status(),
		Took:   now.Sub(avatar.CreatedAt()),
	}
}
