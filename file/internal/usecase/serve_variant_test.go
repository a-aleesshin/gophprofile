package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
)

type stubServingPolicies struct {
	policy ServingPolicy
	err    error
}

func (s stubServingPolicies) ServingPolicy(domain.Namespace) (ServingPolicy, error) {
	return s.policy, s.err
}

type stubServableVariants struct {
	view ServableVariantView
	err  error
}

func (s *stubServableVariants) ServableByName(
	context.Context, domain.FileID, domain.VariantName,
) (ServableVariantView, error) {
	return s.view, s.err
}

const testETag = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func newServeHarness(t *testing.T) (*ServeVariantHandler, *stubObjectReadWriter, ServeVariantCommand) {
	t.Helper()

	id, err := domain.NewFileID()
	require.NoError(t, err)

	namespace, err := domain.NewNamespace("avatar")
	require.NoError(t, err)

	name, err := domain.NewVariantName("300x300")
	require.NoError(t, err)

	ext, err := domain.NewExtension("webp")
	require.NoError(t, err)

	location, err := domain.NewVariantLocation(id, name, ext)
	require.NoError(t, err)

	objects := &stubObjectReadWriter{}

	handler := NewServeVariantHandler(
		stubServingPolicies{policy: ServingPolicy{
			CacheControl: "public, max-age=31536000, immutable",
			Disposition:  "inline",
		}},
		&stubServableVariants{view: ServableVariantView{
			Namespace: namespace,
			Location:  location,
			Ext:       "webp",
			MimeType:  "image/webp",
			SizeBytes: 4096,
			ETag:      testETag,
			CreatedAt: testNow,
		}},
		objects,
	)

	return handler, objects, ServeVariantCommand{FileID: id, Variant: name, Ext: ext}
}

func TestServeVariant(t *testing.T) {
	t.Parallel()

	t.Run("streams the variant with policy headers", func(t *testing.T) {
		t.Parallel()

		handler, _, cmd := newServeHarness(t)

		got, err := handler.Handle(context.Background(), cmd)
		require.NoError(t, err)

		assert.False(t, got.NotModified)
		require.NotNil(t, got.Body)
		assert.Equal(t, testETag, got.ETag,
			"the etag is our own checksum, not the one object storage reports")
		assert.Equal(t, "public, max-age=31536000, immutable", got.CacheControl)
		assert.Equal(t, "inline", got.Disposition)
		assert.Equal(t, "image/webp", got.MimeType)
		assert.Equal(t, int64(4096), got.SizeBytes)
	})

	t.Run("matching if-none-match never touches storage", func(t *testing.T) {
		t.Parallel()

		handler, objects, cmd := newServeHarness(t)
		cmd.IfNoneMatch = testETag

		got, err := handler.Handle(context.Background(), cmd)
		require.NoError(t, err)

		assert.True(t, got.NotModified)
		assert.Nil(t, got.Body)
		assert.Empty(t, objects.put)
		assert.NotEmpty(t, got.CacheControl, "a 304 still carries the caching headers")
	})

	t.Run("extension mismatch looks exactly like a missing variant", func(t *testing.T) {
		t.Parallel()

		handler, _, cmd := newServeHarness(t)

		jpg, err := domain.NewExtension("jpg")
		require.NoError(t, err)

		cmd.Ext = jpg

		_, err = handler.Handle(context.Background(), cmd)
		require.ErrorIs(t, err, domain.ErrVariantAbsent,
			"serving webp bytes under a .jpg url would lie about the content type")
	})

	t.Run("rejects an incomplete command", func(t *testing.T) {
		t.Parallel()

		handler, _, cmd := newServeHarness(t)

		for _, incomplete := range []ServeVariantCommand{
			{},
			{FileID: cmd.FileID},
			{FileID: cmd.FileID, Variant: cmd.Variant},
		} {
			_, err := handler.Handle(context.Background(), incomplete)
			require.ErrorIs(t, err, ErrInvalidCommand)
		}
	})
}
