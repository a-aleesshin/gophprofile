//go:build integration

package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	miniogo "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
)

const testImage = "minio/minio:latest"

var (
	sharedEndpoint string
	sharedUser     string
	sharedPassword string
	bucketCounter  atomic.Uint64
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	container, err := tcminio.Run(ctx, testImage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run minio: %v\n", err)
		os.Exit(1)
	}

	sharedEndpoint, err = container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connection string: %v\n", err)
		os.Exit(1)
	}

	sharedUser, sharedPassword = container.Username, container.Password

	code := m.Run()

	if err := container.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "terminate minio: %v\n", err)
	}

	os.Exit(code)
}

func newTestStore(t *testing.T) (*Store, Config) {
	t.Helper()

	bucket := fmt.Sprintf("gophprofile-%d", bucketCounter.Add(1))

	admin, err := miniogo.New(sharedEndpoint, &miniogo.Options{
		Creds: credentials.NewStaticV4(sharedUser, sharedPassword, ""),
	})
	require.NoError(t, err)
	require.NoError(t, admin.MakeBucket(t.Context(), bucket, miniogo.MakeBucketOptions{}))

	cfg := Config{
		Endpoint:   sharedEndpoint,
		PublicBase: "http://" + sharedEndpoint,
		AccessKey:  sharedUser,
		SecretKey:  sharedPassword,
		Bucket:     bucket,
		Region:     "us-east-1",
	}

	store, err := New(cfg, pgtx.New(nil))
	require.NoError(t, err)

	return store, cfg
}

func testLocation(t *testing.T, key string) domain.Location {
	t.Helper()

	location, err := domain.RestoreLocation(key)
	require.NoError(t, err)

	return location
}

func testPutParams(t *testing.T, key, mime string, body []byte) usecase.PutObjectParams {
	t.Helper()

	mimeType, err := domain.NewMimeType(mime)
	require.NoError(t, err)

	size, err := domain.NewSize(int64(len(body)))
	require.NoError(t, err)

	return usecase.PutObjectParams{
		Location: testLocation(t, key),
		Body:     bytes.NewReader(body),
		MimeType: mimeType,
		Size:     size,
	}
}

func TestStore_PutHeadGet(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	ctx := context.Background()

	body := []byte("variant bytes")
	require.NoError(t, store.Put(ctx, testPutParams(t, "variants/a/300x300.webp", "image/webp", body)))

	info, err := store.Head(ctx, testLocation(t, "variants/a/300x300.webp"))
	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), info.SizeBytes)
	assert.Equal(t, "image/webp", info.ContentType)

	reader, err := store.Get(ctx, testLocation(t, "variants/a/300x300.webp"))
	require.NoError(t, err)

	defer func() { require.NoError(t, reader.Close()) }()

	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestStore_MissingObject(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	ctx := context.Background()
	absent := testLocation(t, "staging/does-not-exist")

	_, err := store.Head(ctx, absent)
	require.ErrorIs(t, err, usecase.ErrObjectNotFound)

	_, err = store.Get(ctx, absent)
	require.ErrorIs(t, err, usecase.ErrObjectNotFound,
		"Get must fail eagerly: minio-go defers the request until the first read")
}

func TestStore_Copy(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	ctx := context.Background()

	body := []byte("original bytes")
	require.NoError(t, store.Put(ctx, testPutParams(t, "staging/ticket", "image/jpeg", body)))

	require.NoError(t, store.Copy(ctx,
		testLocation(t, "staging/ticket"),
		testLocation(t, "originals/file"),
	))

	info, err := store.Head(ctx, testLocation(t, "originals/file"))
	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), info.SizeBytes)
	assert.Equal(t, "image/jpeg", info.ContentType, "copy preserves the content type")
}

func TestStore_DeleteIsIdempotent(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	ctx := context.Background()

	present := testLocation(t, "variants/b/100x100.webp")
	absent := testLocation(t, "variants/b/300x300.webp")

	require.NoError(t, store.Put(ctx, testPutParams(t, present.String(), "image/webp", []byte("x"))))

	require.NoError(t, store.Delete(ctx, present, absent),
		"deleting an absent object must succeed: the delete command is retried on failure")

	_, err := store.Head(ctx, present)
	require.ErrorIs(t, err, usecase.ErrObjectNotFound)

	require.NoError(t, store.Delete(ctx, present), "a repeated delete is a no-op")
	require.NoError(t, store.Delete(ctx))
}

func postPresigned(
	t *testing.T, presigned usecase.PresignedUpload, body []byte, contentType string,
) (int, string) {
	t.Helper()

	var buf bytes.Buffer

	form := multipart.NewWriter(&buf)

	fields := make(map[string]string, len(presigned.Fields))
	for field, value := range presigned.Fields {
		fields[field] = value
	}

	if contentType != "" {
		fields["Content-Type"] = contentType
	}

	for field, value := range fields {
		require.NoError(t, form.WriteField(field, value))
	}

	part, err := form.CreateFormFile("file", "upload.bin")
	require.NoError(t, err)

	_, err = part.Write(body)
	require.NoError(t, err)
	require.NoError(t, form.Close())

	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, presigned.URL, &buf,
	)
	require.NoError(t, err)

	request.Header.Set("Content-Type", form.FormDataContentType())

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)

	defer func() { require.NoError(t, response.Body.Close()) }()

	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return response.StatusCode, string(payload)
}

func TestStore_PresignUploadEnforcesThePolicy(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t)
	ctx := context.Background()

	const maxSize = 1024

	key := testLocation(t, "staging/presigned-ticket")

	presigned, err := store.PresignUpload(ctx, usecase.PresignUploadParams{
		Location:          key,
		MaxSizeBytes:      maxSize,
		ContentTypePrefix: "image/",
		ExpiresAt:         time.Now().Add(10 * time.Minute),
	})
	require.NoError(t, err)

	require.NotEmpty(t, presigned.URL)
	require.NotEmpty(t, presigned.Fields)
	assert.Equal(t, key.String(), presigned.Fields["key"],
		"the key is fixed by the policy, so a client cannot write anywhere else")
	assert.Equal(t, "image/", presigned.Fields["Content-Type"],
		"the prefix condition seeds the field; a client overwrites it and must not append a second value")

	t.Run("within the limit is accepted", func(t *testing.T) {
		status, payload := postPresigned(t, presigned, bytes.Repeat([]byte("a"), 512), "image/jpeg")
		require.Equal(t, http.StatusNoContent, status, "minio said: %s", payload)

		info, err := store.Head(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, int64(512), info.SizeBytes)
	})

	t.Run("over the limit is rejected by storage", func(t *testing.T) {
		status, _ := postPresigned(t, presigned, bytes.Repeat([]byte("a"), maxSize+1), "image/jpeg")
		assert.GreaterOrEqual(t, status, 400,
			"content-length-range is the only reason we chose presigned POST over PUT")
	})

	t.Run("wrong content type is rejected by storage", func(t *testing.T) {
		status, _ := postPresigned(t, presigned, []byte("short"), "application/pdf")
		assert.GreaterOrEqual(t, status, 400)
	})
}

func TestStore_PresignUploadUsesThePublicBase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, cfg := newTestStore(t)

	cfg.PublicBase = "https://domain.com/s3"

	rewritten, err := New(cfg, pgtx.New(nil))
	require.NoError(t, err)

	presigned, err := rewritten.PresignUpload(ctx, usecase.PresignUploadParams{
		Location:     testLocation(t, "staging/ticket"),
		MaxSizeBytes: 1024,
		ExpiresAt:    time.Now().Add(time.Minute),
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(presigned.URL, "https://domain.com/s3/"+cfg.Bucket),
		"the browser gets a public url even though the client talks to the internal endpoint")
}

func TestNew_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()

	full := Config{
		Endpoint: "minio:9000", PublicBase: "https://domain.com/s3",
		AccessKey: "key", SecretKey: "secret", Bucket: "gophprofile",
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "endpoint", mutate: func(c *Config) { c.Endpoint = "" }},
		{name: "public base", mutate: func(c *Config) { c.PublicBase = "" }},
		{name: "bucket", mutate: func(c *Config) { c.Bucket = "" }},
		{name: "access key", mutate: func(c *Config) { c.AccessKey = "" }},
		{name: "secret key", mutate: func(c *Config) { c.SecretKey = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			tt.mutate(&cfg)

			_, err := New(cfg, pgtx.New(nil))
			require.ErrorIs(t, err, ErrInvalidConfig)
		})
	}

	_, err := New(full, pgtx.New(nil))
	require.NoError(t, err)
}
