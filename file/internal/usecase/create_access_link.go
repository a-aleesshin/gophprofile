package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

var ErrFileNotServable = errors.New("file is not servable")

type AccessPolicy struct {
	Signed     bool
	MaxLinkTTL time.Duration
}

type LinkTargetView struct {
	Namespace domain.Namespace
	Variant   string
	Ext       string
	Status    domain.FileStatus
	Deleted   bool
}

type BuildLinkParams struct {
	FileID    string
	Variant   string
	Ext       string
	ExpiresAt time.Time
}

type accessPolicyProvider interface {
	AccessPolicy(namespace domain.Namespace) (AccessPolicy, error)
}

type linkTargetLoader interface {
	LinkTargetByName(
		ctx context.Context,
		fileID domain.FileID,
		name domain.VariantName,
	) (LinkTargetView, error)
}

type linkBuilder interface {
	BuildLink(ctx context.Context, params BuildLinkParams) (string, error)
}

type CreateAccessLinkCommand struct {
	FileID  domain.FileID
	Variant domain.VariantName
	TTL     time.Duration
}

type CreateAccessLinkResult struct {
	URL       string
	ExpiresAt time.Time
}

type CreateAccessLinkHandler struct {
	policies accessPolicyProvider
	targets  linkTargetLoader
	links    linkBuilder
	clock    clock
}

func NewCreateAccessLinkHandler(
	policies accessPolicyProvider,
	targets linkTargetLoader,
	links linkBuilder,
	clock clock,
) *CreateAccessLinkHandler {
	return &CreateAccessLinkHandler{
		policies: policies,
		targets:  targets,
		links:    links,
		clock:    clock,
	}
}

func (h *CreateAccessLinkHandler) Handle(
	ctx context.Context,
	cmd CreateAccessLinkCommand,
) (CreateAccessLinkResult, error) {
	switch {
	case cmd.FileID.IsZero():
		return CreateAccessLinkResult{}, fmt.Errorf("%w: file id is required", ErrInvalidCommand)
	case cmd.Variant.IsZero():
		return CreateAccessLinkResult{}, fmt.Errorf("%w: variant is required", ErrInvalidCommand)
	}

	target, err := h.targets.LinkTargetByName(ctx, cmd.FileID, cmd.Variant)
	if err != nil {
		return CreateAccessLinkResult{}, err
	}

	if target.Deleted || !target.Status.IsServable() {
		return CreateAccessLinkResult{}, fmt.Errorf(
			"%w: status %q", ErrFileNotServable, target.Status.String(),
		)
	}

	policy, err := h.policies.AccessPolicy(target.Namespace)
	if err != nil {
		return CreateAccessLinkResult{}, fmt.Errorf(
			"%w: %q", ErrUnknownNamespace, target.Namespace.String(),
		)
	}

	var expiresAt time.Time

	if policy.Signed {
		ttl := policy.MaxLinkTTL
		if cmd.TTL > 0 && cmd.TTL < ttl {
			ttl = cmd.TTL
		}

		if ttl <= 0 {
			return CreateAccessLinkResult{}, fmt.Errorf(
				"%w: namespace %q has no link ttl", ErrInvalidCommand, target.Namespace.String(),
			)
		}

		expiresAt = h.clock.Now().Add(ttl).UTC()
	}

	url, err := h.links.BuildLink(ctx, BuildLinkParams{
		FileID:    cmd.FileID.String(),
		Variant:   target.Variant,
		Ext:       target.Ext,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return CreateAccessLinkResult{}, err
	}

	return CreateAccessLinkResult{URL: url, ExpiresAt: expiresAt}, nil
}
