package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubAccessPolicies struct {
	policy AccessPolicy
	err    error
}

func (s stubAccessPolicies) AccessPolicy(domain.Namespace) (AccessPolicy, error) {
	return s.policy, s.err
}

type stubLinkTargets struct {
	view LinkTargetView
	err  error
}

func (s *stubLinkTargets) LinkTargetByName(
	context.Context, domain.FileID, domain.VariantName,
) (LinkTargetView, error) {
	return s.view, s.err
}

type stubLinkBuilder struct {
	params BuildLinkParams
	calls  int
}

func (s *stubLinkBuilder) BuildLink(_ context.Context, params BuildLinkParams) (string, error) {
	s.calls++
	s.params = params

	return "https://domain.com/upload/" + params.FileID + "/" + params.Variant + "." + params.Ext, nil
}

func newAccessLinkHarness(
	t *testing.T, policy AccessPolicy, status domain.FileStatus, deleted bool,
) (*CreateAccessLinkHandler, *stubLinkBuilder, CreateAccessLinkCommand) {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	name, err := domain.NewVariantName("300x300")
	require.NoError(t, err)

	links := &stubLinkBuilder{}

	handler := NewCreateAccessLinkHandler(
		stubAccessPolicies{policy: policy},
		&stubLinkTargets{view: LinkTargetView{
			Namespace: namespace,
			Variant:   "300x300",
			Ext:       "webp",
			Status:    status,
			Deleted:   deleted,
		}},
		links,
		fixedClock{now: testNow},
	)

	return handler, links, CreateAccessLinkCommand{FileID: id, Variant: name}
}

func TestCreateAccessLink(t *testing.T) {
	t.Parallel()

	t.Run("signed namespace gets a deadline", func(t *testing.T) {
		t.Parallel()

		handler, links, cmd := newAccessLinkHarness(t,
			AccessPolicy{Signed: true, MaxLinkTTL: 15 * time.Minute},
			domain.FileStatusReady, false)

		got, err := handler.Handle(context.Background(), cmd)
		require.NoError(t, err)

		assert.Equal(t, testNow.Add(15*time.Minute), got.ExpiresAt)
		assert.NotEmpty(t, got.URL)
		assert.Equal(t, got.ExpiresAt, links.params.ExpiresAt)
		assert.Equal(t, "webp", links.params.Ext)
	})

	t.Run("requested ttl may only shorten the link", func(t *testing.T) {
		t.Parallel()

		policy := AccessPolicy{Signed: true, MaxLinkTTL: 15 * time.Minute}

		handler, _, cmd := newAccessLinkHarness(t, policy, domain.FileStatusReady, false)
		cmd.TTL = time.Minute

		got, err := handler.Handle(context.Background(), cmd)
		require.NoError(t, err)
		assert.Equal(t, testNow.Add(time.Minute), got.ExpiresAt)

		handler, _, cmd = newAccessLinkHarness(t, policy, domain.FileStatusReady, false)
		cmd.TTL = time.Hour

		got, err = handler.Handle(context.Background(), cmd)
		require.NoError(t, err)
		assert.Equal(t, testNow.Add(15*time.Minute), got.ExpiresAt,
			"a caller cannot extend a link beyond the policy")
	})

	t.Run("public namespace returns a stable link", func(t *testing.T) {
		t.Parallel()

		handler, links, cmd := newAccessLinkHarness(t,
			AccessPolicy{Signed: false}, domain.FileStatusReady, false)
		cmd.TTL = time.Minute

		got, err := handler.Handle(context.Background(), cmd)
		require.NoError(t, err)

		assert.True(t, got.ExpiresAt.IsZero(), "a public link has no deadline to carry")
		assert.True(t, links.params.ExpiresAt.IsZero(),
			"a zero deadline tells the builder not to sign")
	})

	t.Run("not servable", func(t *testing.T) {
		t.Parallel()

		for _, tt := range []struct {
			name    string
			status  domain.FileStatus
			deleted bool
		}{
			{name: "processing", status: domain.FileStatusProcessing},
			{name: "rejected", status: domain.FileStatusRejected},
			{name: "deleted", status: domain.FileStatusReady, deleted: true},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				handler, links, cmd := newAccessLinkHarness(t,
					AccessPolicy{Signed: true, MaxLinkTTL: time.Minute}, tt.status, tt.deleted)

				_, err := handler.Handle(context.Background(), cmd)
				require.ErrorIs(t, err, ErrFileNotServable)
				assert.Zero(t, links.calls)
			})
		}
	})

	t.Run("rejects an empty command", func(t *testing.T) {
		t.Parallel()

		handler, _, cmd := newAccessLinkHarness(t, AccessPolicy{}, domain.FileStatusReady, false)

		_, err := handler.Handle(context.Background(), CreateAccessLinkCommand{})
		require.ErrorIs(t, err, ErrInvalidCommand)

		_, err = handler.Handle(context.Background(), CreateAccessLinkCommand{FileID: cmd.FileID})
		require.ErrorIs(t, err, ErrInvalidCommand)
	})
}

func TestCreateAccessLink_OriginalGetsASignedLinkInAPublicNamespace(t *testing.T) {
	t.Parallel()

	// Arrange
	handler, links, cmd := newAccessLinkHarness(t,
		AccessPolicy{
			Signed:         false,
			SignedVariants: []domain.VariantName{domain.VariantNameOriginal},
			MaxLinkTTL:     15 * time.Minute,
		},
		domain.FileStatusReady, false)

	cmd.Variant = domain.VariantNameOriginal

	// Act
	got, err := handler.Handle(context.Background(), cmd)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, testNow.Add(15*time.Minute), got.ExpiresAt,
		"a stable url for the original would be refused by the serving endpoint")
	assert.Equal(t, got.ExpiresAt, links.params.ExpiresAt)
}

func TestCreateAccessLink_ASignedVariantNeedsATtl(t *testing.T) {
	t.Parallel()

	// Arrange
	handler, _, cmd := newAccessLinkHarness(t,
		AccessPolicy{
			SignedVariants: []domain.VariantName{domain.VariantNameOriginal},
		},
		domain.FileStatusReady, false)

	cmd.Variant = domain.VariantNameOriginal

	// Act
	_, err := handler.Handle(context.Background(), cmd)

	// Assert
	require.ErrorIs(t, err, ErrInvalidCommand,
		"defence in depth: the policy loader now refuses such a configuration outright, "+
			"so reaching this branch means the policy came from somewhere else")
}
