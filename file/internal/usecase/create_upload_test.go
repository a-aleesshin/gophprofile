package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

var (
	testNow         = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	testPolicyLimit = int64(10 << 20)
	testPolicyTTL   = 15 * time.Minute
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type stubPolicies struct {
	policy UploadPolicy
	err    error
}

func (s stubPolicies) UploadPolicy(domain.Namespace) (UploadPolicy, error) {
	return s.policy, s.err
}

type stubFinder struct {
	results []finderResult
	calls   int
}

type finderResult struct {
	upload domain.Upload
	err    error
}

func (s *stubFinder) ByIdempotencyKey(
	context.Context, domain.Namespace, domain.IdempotencyKey,
) (domain.Upload, error) {
	s.calls++

	if len(s.results) == 0 {
		return domain.Upload{}, domain.ErrUploadNotFound
	}

	next := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}

	return next.upload, next.err
}

type stubCreator struct {
	created []domain.Upload
	err     error
}

func (s *stubCreator) Create(_ context.Context, upload *domain.Upload) error {
	s.created = append(s.created, *upload)

	return s.err
}

type stubPresigner struct {
	params PresignUploadParams
	calls  int
	err    error
}

func (s *stubPresigner) PresignUpload(
	_ context.Context, params PresignUploadParams,
) (PresignedUpload, error) {
	s.calls++
	s.params = params

	if s.err != nil {
		return PresignedUpload{}, s.err
	}

	return PresignedUpload{
		URL:    "https://domain.com/s3/gophprofile",
		Fields: map[string]string{"key": params.Location.String()},
	}, nil
}

func testUploadPolicy(t *testing.T) UploadPolicy {
	t.Helper()

	jpeg, err := domain.NewMimeType("image/jpeg")
	require.NoError(t, err)

	png, err := domain.NewMimeType("image/png")
	require.NoError(t, err)

	allowed, err := domain.NewMimeSet(jpeg, png)
	require.NoError(t, err)

	return UploadPolicy{
		MaxSizeBytes:      testPolicyLimit,
		AllowedMime:       allowed,
		ContentTypePrefix: "image/",
		TTL:               testPolicyTTL,
	}
}

func testCommand(t *testing.T) CreateUploadCommand {
	t.Helper()

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	ownerRef, err := domain.NewOwnerRef("user-123")
	require.NoError(t, err)

	originalName, err := domain.NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	declaredMime, err := domain.NewMimeType("image/jpeg")
	require.NoError(t, err)

	maxSize, err := domain.NewSize(1 << 20)
	require.NoError(t, err)

	key, err := domain.NewIdempotencyKey("01a18818-c65b-732b-9e0a-e8603949ed3d")
	require.NoError(t, err)

	return CreateUploadCommand{
		Namespace:      namespace,
		OwnerRef:       ownerRef,
		OriginalName:   originalName,
		DeclaredMime:   declaredMime,
		MaxSize:        maxSize,
		IdempotencyKey: key,
	}
}

func testSize(t *testing.T, bytes int64) domain.Size {
	t.Helper()

	size, err := domain.NewSize(bytes)
	require.NoError(t, err)

	return size
}

type harness struct {
	handler   *CreateUploadHandler
	finder    *stubFinder
	creator   *stubCreator
	presigner *stubPresigner
}

func newHarness(t *testing.T, policy UploadPolicy, policyErr error, results ...finderResult) harness {
	t.Helper()

	finder := &stubFinder{results: results}
	creator := &stubCreator{}
	presigner := &stubPresigner{}

	return harness{
		handler: NewCreateUploadHandler(
			stubPolicies{policy: policy, err: policyErr},
			finder,
			creator,
			presigner,
			fixedClock{now: testNow},
		),
		finder:    finder,
		creator:   creator,
		presigner: presigner,
	}
}

func TestCreateUpload_CreatesTicket(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testUploadPolicy(t), nil)

	got, err := h.handler.Handle(context.Background(), testCommand(t))
	require.NoError(t, err)

	require.Len(t, h.creator.created, 1)
	created := h.creator.created[0]

	assert.True(t, created.IsPending())
	assert.Equal(t, int64(1), created.Version())
	assert.Equal(t, "avatar", created.Namespace().String())
	assert.Equal(t, "user-123", created.OwnerRef().String())
	assert.Equal(t, "avatar.jpg", created.OriginalName().String())
	assert.Equal(t, "image/jpeg", created.DeclaredMime().String())
	assert.Equal(t, testNow.Add(testPolicyTTL), created.ExpiresAt())

	assert.True(t, got.UploadID.Equal(created.ID()))
	assert.Equal(t, testNow.Add(testPolicyTTL), got.ExpiresAt)
	assert.NotEmpty(t, got.URL)
	assert.Equal(t, created.Location().String(), got.Fields["key"])

	assert.Equal(t, 1, h.presigner.calls)
	assert.Equal(t, "image/", h.presigner.params.ContentTypePrefix)
	assert.Equal(t, created.Location().String(), h.presigner.params.Location.String())
	assert.Equal(t, created.ExpiresAt(), h.presigner.params.ExpiresAt)
}

func TestCreateUpload_ClampsMaxSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested func(*testing.T) domain.Size
		want      int64
	}{
		{
			name:      "below the limit is kept",
			requested: func(t *testing.T) domain.Size { return testSize(t, 1<<20) },
			want:      1 << 20,
		},
		{
			name:      "above the limit is clamped",
			requested: func(t *testing.T) domain.Size { return testSize(t, 1<<30) },
			want:      testPolicyLimit,
		},
		{
			name:      "unset falls back to the limit",
			requested: func(*testing.T) domain.Size { return domain.Size{} },
			want:      testPolicyLimit,
		},
		{
			name:      "exactly the limit is kept",
			requested: func(t *testing.T) domain.Size { return testSize(t, testPolicyLimit) },
			want:      testPolicyLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testUploadPolicy(t), nil)

			cmd := testCommand(t)
			cmd.MaxSize = tt.requested(t)

			_, err := h.handler.Handle(context.Background(), cmd)
			require.NoError(t, err)

			require.Len(t, h.creator.created, 1)
			assert.Equal(t, tt.want, h.creator.created[0].MaxSize().Bytes())
			assert.Equal(t, tt.want, h.presigner.params.MaxSizeBytes)
		})
	}
}

func TestCreateUpload_DeclaredMime(t *testing.T) {
	t.Parallel()

	t.Run("optional", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t, testUploadPolicy(t), nil)

		cmd := testCommand(t)
		cmd.DeclaredMime = domain.MimeType{}

		_, err := h.handler.Handle(context.Background(), cmd)
		require.NoError(t, err)
		assert.True(t, h.creator.created[0].DeclaredMime().IsZero())
	})

	t.Run("outside the policy allowlist", func(t *testing.T) {
		t.Parallel()

		gif, err := domain.NewMimeType("image/gif")
		require.NoError(t, err)

		h := newHarness(t, testUploadPolicy(t), nil)

		cmd := testCommand(t)
		cmd.DeclaredMime = gif

		_, err = h.handler.Handle(context.Background(), cmd)
		require.ErrorIs(t, err, ErrUnsupportedMimeType)
		assert.Empty(t, h.creator.created)
		assert.Zero(t, h.presigner.calls)
	})
}

func TestCreateUpload_RequiresNamespaceAndKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*CreateUploadCommand)
	}{
		{name: "namespace", mutate: func(c *CreateUploadCommand) { c.Namespace = domain.Namespace{} }},
		{name: "idempotency key", mutate: func(c *CreateUploadCommand) { c.IdempotencyKey = domain.IdempotencyKey{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testUploadPolicy(t), nil)

			cmd := testCommand(t)
			tt.mutate(&cmd)

			_, err := h.handler.Handle(context.Background(), cmd)
			require.ErrorIs(t, err, ErrInvalidCommand)
			assert.Zero(t, h.finder.calls)
			assert.Empty(t, h.creator.created)
		})
	}
}

func TestCreateUpload_MissingAggregateFieldsAreRejectedByTheDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*CreateUploadCommand)
	}{
		{name: "owner ref", mutate: func(c *CreateUploadCommand) { c.OwnerRef = domain.OwnerRef{} }},
		{name: "original name", mutate: func(c *CreateUploadCommand) { c.OriginalName = domain.OriginalName{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testUploadPolicy(t), nil)

			cmd := testCommand(t)
			tt.mutate(&cmd)

			_, err := h.handler.Handle(context.Background(), cmd)
			require.ErrorIs(t, err, domain.ErrInvalidUpload)
			assert.Empty(t, h.creator.created)
			assert.Zero(t, h.presigner.calls)
		})
	}
}

func TestCreateUpload_UnknownNamespace(t *testing.T) {
	t.Parallel()

	h := newHarness(t, UploadPolicy{}, errors.New("no such namespace"))

	_, err := h.handler.Handle(context.Background(), testCommand(t))
	require.ErrorIs(t, err, ErrUnknownNamespace)
	assert.Zero(t, h.finder.calls)
	assert.Empty(t, h.creator.created)
}

func testPendingUpload(t *testing.T, now time.Time) domain.Upload {
	t.Helper()

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	owner, err := domain.NewOwnerRef("user-123")
	require.NoError(t, err)

	key, err := domain.NewIdempotencyKey("01a18818-c65b-732b-9e0a-e8603949ed3d")
	require.NoError(t, err)

	name, err := domain.NewOriginalName("avatar.jpg")
	require.NoError(t, err)

	size, err := domain.NewSize(testPolicyLimit)
	require.NoError(t, err)

	id, err := domain.NewUploadID()
	require.NoError(t, err)

	u, err := domain.NewUpload(domain.NewUploadParams{
		ID:             id,
		Namespace:      namespace,
		OwnerRef:       owner,
		IdempotencyKey: key,
		OriginalName:   name,
		MaxSize:        size,
		TTL:            testPolicyTTL,
		Now:            now,
	})
	require.NoError(t, err)

	return u
}

func TestCreateUpload_Idempotency(t *testing.T) {
	t.Parallel()

	t.Run("reissues the pending ticket", func(t *testing.T) {
		t.Parallel()

		existing := testPendingUpload(t, testNow.Add(-time.Minute))
		h := newHarness(t, testUploadPolicy(t), nil, finderResult{upload: existing})

		got, err := h.handler.Handle(context.Background(), testCommand(t))
		require.NoError(t, err)

		assert.Empty(t, h.creator.created, "a repeated call must not create a second ticket")
		assert.True(t, got.UploadID.Equal(existing.ID()))
		assert.Equal(t, existing.ExpiresAt(), got.ExpiresAt)
		assert.Equal(t, existing.ExpiresAt(), h.presigner.params.ExpiresAt,
			"the ticket keeps its original deadline")
	})

	t.Run("expired ticket is not reusable", func(t *testing.T) {
		t.Parallel()

		existing := testPendingUpload(t, testNow.Add(-time.Hour))
		h := newHarness(t, testUploadPolicy(t), nil, finderResult{upload: existing})

		_, err := h.handler.Handle(context.Background(), testCommand(t))
		require.ErrorIs(t, err, ErrUploadNotReusable)
		assert.Empty(t, h.creator.created)
		assert.Zero(t, h.presigner.calls)
	})

	t.Run("used ticket is not reusable", func(t *testing.T) {
		t.Parallel()

		existing := testPendingUpload(t, testNow)
		fileID, err := domain.NewFileID()
		require.NoError(t, err)
		require.NoError(t, existing.Complete(fileID, testNow))

		h := newHarness(t, testUploadPolicy(t), nil, finderResult{upload: existing})

		_, err = h.handler.Handle(context.Background(), testCommand(t))
		require.ErrorIs(t, err, ErrUploadNotReusable)
		assert.Zero(t, h.presigner.calls)
	})

	t.Run("concurrent create loses the race and reuses the winner", func(t *testing.T) {
		t.Parallel()

		existing := testPendingUpload(t, testNow)
		h := newHarness(t, testUploadPolicy(t), nil,
			finderResult{err: domain.ErrUploadNotFound},
			finderResult{upload: existing},
		)
		h.creator.err = domain.ErrUploadAlreadyExists

		got, err := h.handler.Handle(context.Background(), testCommand(t))
		require.NoError(t, err)
		assert.True(t, got.UploadID.Equal(existing.ID()))
		assert.Equal(t, 2, h.finder.calls)
		assert.Equal(t, 2, h.presigner.calls,
			"the loser's signature points at its own staging key and cannot be reused")
		assert.Equal(t, existing.Location().String(), h.presigner.params.Location.String())
	})
}

func TestCreateUpload_PropagatesInfrastructureErrors(t *testing.T) {
	t.Parallel()

	t.Run("finder", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("connection refused")
		h := newHarness(t, testUploadPolicy(t), nil, finderResult{err: boom})

		_, err := h.handler.Handle(context.Background(), testCommand(t))
		require.ErrorIs(t, err, boom)
		assert.Empty(t, h.creator.created)
	})

	t.Run("creator", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("deadline exceeded")
		h := newHarness(t, testUploadPolicy(t), nil)
		h.creator.err = boom

		_, err := h.handler.Handle(context.Background(), testCommand(t))
		require.ErrorIs(t, err, boom)
		assert.Equal(t, 1, h.presigner.calls,
			"the signature is produced before the insert, so a failed insert leaves no row")
	})

	t.Run("presigner", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("clock skew")
		h := newHarness(t, testUploadPolicy(t), nil)
		h.presigner.err = boom

		_, err := h.handler.Handle(context.Background(), testCommand(t))
		require.ErrorIs(t, err, boom)
		assert.Empty(t, h.creator.created,
			"a failed signature must not leave a ticket holding the idempotency key")
	})
}
