package messaging

import (
	"context"
	"errors"
	"fmt"

	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

var (
	ErrOutsideTransaction = errors.New(
		"a fact must be written in the transaction that produced it")
	ErrInvalidFact = errors.New("invalid fact")
)

const DestinationProfileEvents = "gophprofile.profile.v1"

const TypeAvatarDeleted = "AvatarDeleted"

type avatarDeletedData struct {
	AvatarID string `json:"avatar_id"`
	UserID   string `json:"user_id"`
	FileID   string `json:"file_id"`
}

type transactionProbe interface {
	InTransaction(ctx context.Context) bool
}

type Publisher struct {
	writer *outbox.Writer
	guard  transactionProbe
}

func NewPublisher(writer *outbox.Writer, guard transactionProbe) *Publisher {
	return &Publisher{writer: writer, guard: guard}
}

func (p *Publisher) PublishAvatarDeleted(
	ctx context.Context,
	fact usecase.AvatarDeletedFact,
) error {
	if !p.guard.InTransaction(ctx) {
		return fmt.Errorf("%w: %s", ErrOutsideTransaction, TypeAvatarDeleted)
	}

	switch {
	case fact.AvatarID.IsZero():
		return fmt.Errorf("%w: %s needs an avatar id", ErrInvalidFact, TypeAvatarDeleted)
	case fact.UserID.IsZero():
		return fmt.Errorf("%w: %s needs a user id", ErrInvalidFact, TypeAvatarDeleted)
	case fact.FileID.IsZero():
		return fmt.Errorf("%w: %s needs a file id", ErrInvalidFact, TypeAvatarDeleted)
	}

	return p.writer.Write(ctx, outbox.Message{
		Transport:    outbox.TransportKafka,
		Destination:  DestinationProfileEvents,
		PartitionKey: fact.UserID.String(),
		Type:         TypeAvatarDeleted,
		Data: avatarDeletedData{
			AvatarID: fact.AvatarID.String(),
			UserID:   fact.UserID.String(),
			FileID:   fact.FileID.String(),
		},
	})
}
