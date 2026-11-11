//go:build integration

package postgres

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

func TestVariants_CreateAllAndLocations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	file := testFile(t)
	require.NoError(t, files.Create(ctx, &file))

	ext, err := domain.NewExtension("webp")
	require.NoError(t, err)

	webp, err := domain.NewMimeType("image/webp")
	require.NoError(t, err)

	size, err := domain.NewSize(4096)
	require.NoError(t, err)

	dimensions, err := domain.NewDimensions(100, 100)
	require.NoError(t, err)

	batch := make([]domain.Variant, 0, 3)

	for _, name := range []string{"100x100", "300x300"} {
		variantName, nameErr := domain.NewVariantName(name)
		require.NoError(t, nameErr)

		variant, variantErr := domain.NewVariant(domain.NewVariantParams{
			FileID:     file.ID(),
			Name:       variantName,
			Ext:        ext,
			MimeType:   webp,
			Dimensions: dimensions,
			Size:       size,
			Checksum:   testChecksum(t),
			Now:        time.Now(),
		})
		require.NoError(t, variantErr)

		batch = append(batch, variant)
	}

	jpg, err := domain.NewExtension("jpg")
	require.NoError(t, err)

	original, err := domain.NewVariant(domain.NewVariantParams{
		FileID:   file.ID(),
		Name:     domain.VariantNameOriginal,
		Ext:      jpg,
		MimeType: file.MimeType(),
		Size:     file.Size(),
		Checksum: testChecksum(t),
		Now:      time.Now(),
	})
	require.NoError(t, err)

	batch = append(batch, original)

	require.NoError(t, variants.CreateAll(ctx, batch))
	require.NoError(t, variants.CreateAll(ctx, nil), "an empty batch is a no-op")

	locations, err := variants.LocationsByFileID(ctx, file.ID())
	require.NoError(t, err)
	require.Len(t, locations, 3)

	keys := make([]string, 0, len(locations))
	for _, location := range locations {
		keys = append(keys, location.String())
	}

	assert.Contains(t, keys, "originals/"+file.ID().String(),
		"the original variant points at its own prefix, not at variants/")
	assert.Contains(t, keys, "variants/"+file.ID().String()+"/100x100.webp")
}

func TestVariants_DuplicateNameIsRejected(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	file, variant := readyFileWithVariant(t)

	err := NewVariants(sharedDB).CreateAll(ctx, []domain.Variant{variant})
	require.ErrorIs(t, err, domain.ErrVariantAlreadyExists,
		"a caller must be able to tell a duplicate from a lost connection")
	assert.Contains(t, err.Error(), "file_variants_pkey")
	assert.Contains(t, err.Error(), variant.Name().String())
	_ = file
}

func TestFiles_DuplicateIDIsReportedAsConflict(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo := NewFiles(sharedDB)

	file := testFile(t)
	require.NoError(t, repo.Create(ctx, &file))

	err := repo.Create(ctx, &file)
	require.ErrorIs(t, err, domain.ErrFileAlreadyExists)
	assert.Contains(t, err.Error(), "files_pkey")
}

func TestVariants_ViewsByFileIDs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	variants := NewVariants(sharedDB)

	first, _ := readyFileWithVariant(t)
	second, _ := readyFileWithVariant(t)

	views, err := variants.ViewsByFileIDs(ctx, []domain.FileID{first.ID(), second.ID()})
	require.NoError(t, err)

	require.Len(t, views, 2, "one query returns every file's variants, grouped")
	require.Len(t, views[first.ID()], 1)

	view := views[first.ID()][0]
	assert.Equal(t, "300x300", view.Name)
	assert.Equal(t, "webp", view.Ext)
	assert.Equal(t, int32(300), view.Width)
	assert.Equal(t, testChecksum(t).String(), view.ETag)
}

func TestVariants_ViewsKeepNullDimensions(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	file := testFile(t)
	require.NoError(t, files.Create(ctx, &file))

	pdf, err := domain.NewExtension("pdf")
	require.NoError(t, err)

	mime, err := domain.NewMimeType("application/pdf")
	require.NoError(t, err)

	original, err := domain.NewVariant(domain.NewVariantParams{
		FileID:   file.ID(),
		Name:     domain.VariantNameOriginal,
		Ext:      pdf,
		MimeType: mime,
		Size:     file.Size(),
		Checksum: testChecksum(t),
		Now:      time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, variants.CreateAll(ctx, []domain.Variant{original}))

	views, err := variants.ViewsByFileIDs(ctx, []domain.FileID{file.ID()})
	require.NoError(t, err)
	require.Len(t, views[file.ID()], 1)

	assert.Zero(t, views[file.ID()][0].Width, "a document has no dimensions to report")
	assert.Zero(t, views[file.ID()][0].Height)
}

func TestVariants_ServableByName(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	file, variant := readyFileWithVariant(t)

	got, err := variants.ServableByName(ctx, file.ID(), variant.Name())
	require.NoError(t, err)

	assert.Equal(t, "avatar", got.Namespace.String())
	assert.Equal(t, variant.Location().String(), got.Location.String())
	assert.Equal(t, "webp", got.Ext)
	assert.Equal(t, "image/webp", got.MimeType)
	assert.Equal(t, testChecksum(t).String(), got.ETag)

	unknown, err := domain.NewVariantName("1200x1200")
	require.NoError(t, err)

	_, err = variants.ServableByName(ctx, file.ID(), unknown)
	require.ErrorIs(t, err, domain.ErrVariantAbsent)

	require.NoError(t, file.MarkForDeletion(time.Now()))
	require.NoError(t, files.Save(ctx, &file))

	_, err = variants.ServableByName(ctx, file.ID(), variant.Name())
	require.ErrorIs(t, err, domain.ErrVariantAbsent,
		"the join filters deleted files, so the serving path needs no second check")
}

func TestVariants_ServableByNameSkipsUnreadyFiles(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	file := testFile(t)
	require.NoError(t, files.Create(ctx, &file))

	name, err := domain.NewVariantName("300x300")
	require.NoError(t, err)

	ext, err := domain.NewExtension("webp")
	require.NoError(t, err)

	webp, err := domain.NewMimeType("image/webp")
	require.NoError(t, err)

	size, err := domain.NewSize(4096)
	require.NoError(t, err)

	variant, err := domain.NewVariant(domain.NewVariantParams{
		FileID:   file.ID(),
		Name:     name,
		Ext:      ext,
		MimeType: webp,
		Size:     size,
		Checksum: testChecksum(t),
		Now:      time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, variants.CreateAll(ctx, []domain.Variant{variant}))

	_, err = variants.ServableByName(ctx, file.ID(), name)
	require.ErrorIs(t, err, domain.ErrVariantAbsent,
		"a variant row can exist before the file is ready")
}

func TestVariants_LinkTargetByName(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	file, variant := readyFileWithVariant(t)

	got, err := variants.LinkTargetByName(ctx, file.ID(), variant.Name())
	require.NoError(t, err)

	assert.Equal(t, "300x300", got.Variant)
	assert.Equal(t, "webp", got.Ext)
	assert.True(t, got.Status.Equal(domain.FileStatusReady))
	assert.False(t, got.Deleted)

	require.NoError(t, file.MarkForDeletion(time.Now()))
	require.NoError(t, files.Save(ctx, &file))

	got, err = variants.LinkTargetByName(ctx, file.ID(), variant.Name())
	require.NoError(t, err, "unlike serving, link creation needs to tell deleted from missing")
	assert.True(t, got.Deleted)
}

type stubServingPolicies struct{}

func (stubServingPolicies) ServingPolicy(domain.Namespace) (usecase.ServingPolicy, error) {
	return usecase.ServingPolicy{}, nil
}

type stubAccessPolicies struct{}

func (stubAccessPolicies) AccessPolicy(domain.Namespace) (usecase.AccessPolicy, error) {
	return usecase.AccessPolicy{}, nil
}

type stubObjectGetter struct{}

func (stubObjectGetter) Get(context.Context, domain.Location) (io.ReadCloser, error) {
	return nil, nil
}

type stubLinks struct{}

func (stubLinks) BuildLink(context.Context, usecase.BuildLinkParams) (string, error) {
	return "", nil
}

type stubClock struct{}

func (stubClock) Now() time.Time { return time.Now() }

func TestRepositories_SatisfyTheReadPorts(t *testing.T) {
	t.Parallel()

	files, variants := NewFiles(sharedDB), NewVariants(sharedDB)

	assert.NotNil(t, usecase.NewGetFileHandler(files, variants))
	assert.NotNil(t, usecase.NewGetFilesHandler(files, variants))
	assert.NotNil(t, usecase.NewServeVariantHandler(
		stubServingPolicies{}, variants, stubObjectGetter{}))
	assert.NotNil(t, usecase.NewCreateAccessLinkHandler(
		stubAccessPolicies{}, variants, stubLinks{}, stubClock{}))
}
