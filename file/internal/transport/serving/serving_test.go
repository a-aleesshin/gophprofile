package serving

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/file/internal/adapter/link"
	"github.com/a-aleesshin/gophprofile/file/internal/domain"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
)

const (
	testFileID  = "0193abcd-ef01-7a23-8b45-0123456789ab"
	testVariant = "300x300"
	testExt     = "jpg"
	testBody    = "the bytes"
)

type stubServer struct {
	cmd    usecase.ServeVariantCommand
	result usecase.ServeVariantResult
	err    error
	calls  int
}

func (s *stubServer) Handle(
	_ context.Context,
	cmd usecase.ServeVariantCommand,
) (usecase.ServeVariantResult, error) {
	s.cmd = cmd
	s.calls++

	if s.err != nil {
		return usecase.ServeVariantResult{}, s.err
	}

	return s.result, nil
}

type stubVerifier struct {
	params link.VerifyParams
	err    error
}

func (v *stubVerifier) Verify(params link.VerifyParams) error {
	v.params = params

	return v.err
}

func okResult() usecase.ServeVariantResult {
	return usecase.ServeVariantResult{
		Body:         io.NopCloser(strings.NewReader(testBody)),
		MimeType:     "image/jpeg",
		SizeBytes:    int64(len(testBody)),
		ETag:         "abc123",
		LastModified: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		CacheControl: "public, max-age=31536000, immutable",
		Disposition:  "inline",
	}
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func call(t *testing.T, server *stubServer, verifier *stubVerifier, target string) *httptest.ResponseRecorder {
	t.Helper()

	handler, err := NewHandler(server, verifier, Config{Timeout: time.Second}, testLogger())
	require.NoError(t, err)

	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

	return recorder
}

func path(fileID, variant, ext string) string {
	return fmt.Sprintf("/upload/%s/%s.%s", fileID, variant, ext)
}

func TestServesTheVariant(t *testing.T) {
	t.Parallel()

	// Arrange
	server := &stubServer{result: okResult()}

	// Act
	recorder := call(t, server, &stubVerifier{}, path(testFileID, testVariant, testExt))

	// Assert
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, testBody, recorder.Body.String())

	header := recorder.Header()
	assert.Equal(t, "image/jpeg", header.Get("Content-Type"),
		"the type comes from the database, not from the extension in the url")
	assert.Equal(t, "9", header.Get("Content-Length"))
	assert.Equal(t, `"abc123"`, header.Get("ETag"),
		"RFC 9110 wants a quoted-string, an unquoted tag is free for caches to mangle")
	assert.Equal(t, "public, max-age=31536000, immutable", header.Get("Cache-Control"))
	assert.Equal(t, "inline", header.Get("Content-Disposition"))
	assert.Equal(t, "none", header.Get("Accept-Ranges"))
	assert.Equal(t, "nosniff", header.Get("X-Content-Type-Options"))
	assert.Equal(t, "Sun, 01 Nov 2026 12:00:00 GMT", header.Get("Last-Modified"))

	assert.Equal(t, testFileID, server.cmd.FileID.String())
	assert.Equal(t, testVariant, server.cmd.Variant.String())
	assert.Equal(t, testExt, server.cmd.Ext.String())
	assert.False(t, server.cmd.Signed)
}

func TestAnswersNotModified(t *testing.T) {
	t.Parallel()

	// Arrange
	result := okResult()
	result.NotModified = true
	result.Body = nil

	server := &stubServer{result: result}

	handler, err := NewHandler(server, &stubVerifier{}, Config{Timeout: time.Second},
		testLogger())
	require.NoError(t, err)

	mux := http.NewServeMux()
	handler.Register(mux)

	request := httptest.NewRequest(http.MethodGet, path(testFileID, testVariant, testExt), nil)
	request.Header.Set("If-None-Match", `"abc123"`)

	recorder := httptest.NewRecorder()

	// Act
	mux.ServeHTTP(recorder, request)

	// Assert
	assert.Equal(t, http.StatusNotModified, recorder.Code)
	assert.Empty(t, recorder.Body.String())
	assert.Equal(t, `"abc123"`, recorder.Header().Get("ETag"),
		"a 304 must carry the validators again")
	assert.Equal(t, "public, max-age=31536000, immutable",
		recorder.Header().Get("Cache-Control"))
	assert.Equal(t, `"abc123"`, server.cmd.IfNoneMatch)
}

func TestRefusesANonCanonicalPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "uppercase uuid",
			target: path(strings.ToUpper(testFileID), testVariant, testExt),
		},
		{
			name:   "uuid without hyphens",
			target: "/upload/0193abcdef017a238b450123456789ab/300x300.jpg",
		},
		{
			name:   "urn form",
			target: "/upload/urn:uuid:" + testFileID + "/300x300.jpg",
		},
		{
			name:   "a v4 uuid",
			target: path("6f8c3f7e-0e2e-4a1f-9b7a-2c9b1f0a5d31", testVariant, testExt),
		},
		{
			name:   "uppercase variant",
			target: path(testFileID, "300X300", testExt),
		},
		{
			name:   "uppercase extension",
			target: path(testFileID, testVariant, "JPG"),
		},
		{
			name:   "no extension",
			target: "/upload/" + testFileID + "/300x300",
		},
		{
			name:   "empty variant",
			target: "/upload/" + testFileID + "/.jpg",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			server := &stubServer{result: okResult()}

			// Act
			recorder := call(t, server, &stubVerifier{}, test.target)

			// Assert
			assert.Equal(t, http.StatusNotFound, recorder.Code)
			assert.JSONEq(t, `{"error":"not_found"}`, recorder.Body.String())
			assert.Zero(t, server.calls,
				"a non canonical url must not reach the database, it would fragment the cache")
		})
	}
}

func TestVerifiesASignedLink(t *testing.T) {
	t.Parallel()

	// Arrange
	server := &stubServer{result: okResult()}
	verifier := &stubVerifier{}

	target := path(testFileID, testVariant, testExt) + "?exp=1793448000&sig=abc"

	// Act
	recorder := call(t, server, verifier, target)

	// Assert
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, server.cmd.Signed)
	assert.Equal(t, link.VerifyParams{
		FileID:  testFileID,
		Variant: testVariant,
		Ext:     testExt,
		Expires: "1793448000",
		Sig:     "abc",
	}, verifier.params)
}

func TestMapsFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		verifier *stubVerifier
		err      error
		status   int
		reason   string
	}{
		{
			name:     "a forged signature",
			verifier: &stubVerifier{err: link.ErrBadSignature},
			status:   http.StatusForbidden,
			reason:   "bad_signature",
		},
		{
			name:     "an expired link",
			verifier: &stubVerifier{err: link.ErrExpired},
			status:   http.StatusGone,
			reason:   "link_expired",
		},
		{
			name:   "a namespace that needs a signature",
			err:    usecase.ErrSignatureRequired,
			status: http.StatusForbidden,
			reason: "bad_signature",
		},
		{
			name:   "no such variant",
			err:    domain.ErrVariantAbsent,
			status: http.StatusNotFound,
			reason: "not_found",
		},
		{
			name:   "storage did not answer",
			err:    fmt.Errorf("%w: connection refused", usecase.ErrStorageUnavailable),
			status: http.StatusBadGateway,
			reason: "storage_unavailable",
		},
		{
			name:   "the deadline passed",
			err:    context.DeadlineExceeded,
			status: http.StatusGatewayTimeout,
			reason: "timeout",
		},
		{
			name:   "anything else",
			err:    errors.New("boom"),
			status: http.StatusInternalServerError,
			reason: "internal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			server := &stubServer{result: okResult(), err: test.err}

			verifier := test.verifier
			if verifier == nil {
				verifier = &stubVerifier{}
			}

			target := path(testFileID, testVariant, testExt) + "?exp=1&sig=abc"

			// Act
			recorder := call(t, server, verifier, target)

			// Assert
			assert.Equal(t, test.status, recorder.Code)
			assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, test.reason), recorder.Body.String())
			assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
		})
	}
}

func TestNewHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		variants variantServer
		links    signatureVerifier
		timeout  time.Duration
		logger   *slog.Logger
	}{
		{
			name: "no server", links: &stubVerifier{},
			timeout: time.Second, logger: testLogger(),
		},
		{
			name: "no verifier", variants: &stubServer{},
			timeout: time.Second, logger: testLogger(),
		},
		{
			name: "no timeout", variants: &stubServer{},
			links: &stubVerifier{}, logger: testLogger(),
		},
		{
			name: "no logger", variants: &stubServer{},
			links: &stubVerifier{}, timeout: time.Second,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			handler, err := NewHandler(test.variants, test.links,
				Config{Timeout: test.timeout}, test.logger)

			// Assert
			require.ErrorIs(t, err, ErrInvalidHandler)
			assert.Nil(t, handler)
		})
	}
}

func TestServingLogsTheRouteAndSurvivesAPanic(t *testing.T) {
	t.Parallel()

	// Arrange
	var logs bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	handler, err := NewHandler(&stubServer{err: errors.New("boom")}, &stubVerifier{},
		Config{Timeout: time.Second}, logger)
	require.NoError(t, err)

	mux := http.NewServeMux()
	handler.Register(mux)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/upload/0193abcd-ef01-7a23-8b45-0123456789ab/300x300.jpg", nil))

	// Assert
	require.Equal(t, http.StatusInternalServerError, recorder.Code)

	var record map[string]any

	require.NoError(t, json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record),
		"the public endpoint of file used to log nothing at all")
	assert.Equal(t, "http request", record["msg"])
	assert.Equal(t, "ERROR", record["level"])
	assert.Equal(t, "GET /upload/{file_id}/{object}", record["route"])
	assert.Equal(t, float64(http.StatusInternalServerError), record["status"])
}
