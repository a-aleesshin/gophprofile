package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

func TestStartUploadReturnsTheTicket(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	h.start.result = usecase.StartUploadResult{
		AvatarID:  testAvatarID(t),
		Status:    domain.AvatarStatusPending,
		URL:       "https://domain.com/s3/gophprofile",
		Fields:    map[string]string{"key": "staging/ticket", "Content-Type": "image/"},
		ExpiresAt: testNow,
	}

	// Act
	response, payload := h.asUser(t, http.MethodPost, "/api/v1/avatars/uploads",
		`{"file_name":"avatar.jpg","mime_type":"image/jpeg","size_bytes":1048576}`)

	// Assert
	require.Equal(t, http.StatusCreated, response.StatusCode, "body: %s", payload)
	assert.Equal(t, "application/json", response.Header.Get("Content-Type"))

	body := decode[startUploadBody](t, payload)
	assert.Equal(t, testAvatar, body.AvatarID)
	assert.Equal(t, "pending", body.Status)
	assert.Equal(t, "https://domain.com/s3/gophprofile", body.Upload.URL)
	assert.Equal(t, "image/", body.Upload.Fields["Content-Type"],
		"the prefix is handed over as is, the client overwrites it with the real type")
	assert.True(t, body.Upload.ExpiresAt.Equal(testNow))

	assert.Equal(t, testUser, h.start.cmd.UserID.String())
	assert.Equal(t, "avatar.jpg", h.start.cmd.FileName)
	assert.Equal(t, "image/jpeg", h.start.cmd.MimeType)
	assert.Equal(t, int64(1048576), h.start.cmd.SizeBytes)
	assert.True(t, h.start.deadline, "no use case may run without a deadline")
}

func TestStartUploadRefusesABrokenBody(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	response, payload := h.asUser(t, http.MethodPost, "/api/v1/avatars/uploads", `{not json`)

	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.Equal(t, "invalid_body", decode[errorBody](t, payload).Error)
}

func TestOwnedRoutesNeedAUser(t *testing.T) {
	t.Parallel()

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: "/api/v1/avatars/uploads", body: `{}`},
		{method: http.MethodPost, path: "/api/v1/avatars/" + testAvatar + "/complete"},
		{method: http.MethodDelete, path: "/api/v1/avatars/" + testAvatar},
		{method: http.MethodPut, path: "/api/v1/users/" + testUser, body: `{}`},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			absent, payload := h.do(t, route.method, route.path, route.body, nil)
			require.Equal(t, http.StatusUnauthorized, absent.StatusCode)
			assert.Equal(t, "no_user", decode[errorBody](t, payload).Error)

			// Whitespace alone never survives header parsing, so the value that
			// reaches the handler has to be non-empty and still invalid.
			broken, payload := h.do(t, route.method, route.path, route.body,
				map[string]string{HeaderUserID: strings.Repeat("u", 256)})
			require.Equal(t, http.StatusUnauthorized, broken.StatusCode)
			assert.Equal(t, "bad_user", decode[errorBody](t, payload).Error)
		})
	}
}

func TestPublicRoutesWorkWithoutAUser(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.get.result = usecase.GetAvatarResult{Avatar: testReadyView(t)}
	h.resolve.result = usecase.ResolveResult{URL: "https://domain.com/upload/x/300x300.jpg"}
	h.current.result = h.resolve.result
	h.byEmail.result = h.resolve.result

	hash := "0000000000000000000000000000000000000000000000000000000000000000"

	routes := []struct {
		path string
		want int
	}{
		{path: "/api/v1/avatars/" + testAvatar + "/metadata", want: http.StatusOK},
		{path: "/api/v1/avatars/" + testAvatar, want: http.StatusFound},
		{path: "/api/v1/emails/" + hash + "/avatar", want: http.StatusFound},
		{path: "/api/v1/users/" + testUser + "/avatars", want: http.StatusOK},
		{path: "/api/v1/users/" + testUser + "/avatar", want: http.StatusFound},
	}

	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			response, payload := h.do(t, http.MethodGet, route.path, "", nil)

			require.Equal(t, route.want, response.StatusCode, "body: %s", payload)
		})
	}
}

func TestCompleteUploadIsAccepted(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	h.complete.result = usecase.CompleteUploadResult{
		AvatarID: testAvatarID(t),
		Status:   domain.AvatarStatusProcessing,
	}

	// Act
	response, payload := h.asUser(t, http.MethodPost,
		"/api/v1/avatars/"+testAvatar+"/complete", "")

	// Assert
	require.Equal(t, http.StatusAccepted, response.StatusCode, "body: %s", payload)

	body := decode[completeUploadBody](t, payload)
	assert.Equal(t, "processing", body.Status)
	assert.Equal(t, testUser, h.complete.cmd.UserID.String(),
		"ownership is checked against the header, never against the path")
}

func TestAvatarRoutesRejectAnIDThatIsNotAnAvatarID(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	routes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/avatars/not-a-uuid"},
		{method: http.MethodGet, path: "/api/v1/avatars/not-a-uuid/metadata"},
		{method: http.MethodPost, path: "/api/v1/avatars/not-a-uuid/complete"},
		{method: http.MethodDelete, path: "/api/v1/avatars/not-a-uuid"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response, payload := h.asUser(t, route.method, route.path, "")

			require.Equal(t, http.StatusNotFound, response.StatusCode)
			assert.Equal(t, "avatar_not_found", decode[errorBody](t, payload).Error,
				"a malformed id must not reach the database as a query")
		})
	}
}

func TestMetadataCarriesEveryVariantURL(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	view := testReadyView(t)
	h.get.result = usecase.GetAvatarResult{
		Avatar: view,
		URLs: map[string]string{
			"100x100": "https://domain.com/upload/f/100x100.jpg",
			"300x300": "https://domain.com/upload/f/300x300.jpg",
		},
	}

	// Act
	response, payload := h.do(t, http.MethodGet,
		"/api/v1/avatars/"+testAvatar+"/metadata", "", nil)

	// Assert
	require.Equal(t, http.StatusOK, response.StatusCode, "body: %s", payload)

	body := decode[avatarBody](t, payload)
	assert.Equal(t, "ready", body.Status)

	require.NotNil(t, body.File)
	assert.Equal(t, "avatar.jpg", body.File.FileName)
	assert.Equal(t, int32(1920), body.File.Dimensions.Width)

	require.Len(t, body.Variants, 2)
	assert.Equal(t, "https://domain.com/upload/f/300x300.jpg", body.Variants[1].URL)
	assert.Equal(t, int32(300), body.Variants[1].Height)

	assert.True(t, h.get.cmd.UserID.IsZero(),
		"metadata is public by contract, so no ownership check is requested")
}

func TestMetadataOfAPendingAvatarHasNoFile(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	view := testReadyView(t)
	view.Status = domain.AvatarStatusPending
	view.MimeType, view.FileName, view.SizeBytes = "", "", 0
	view.Variants = nil

	h.get.result = usecase.GetAvatarResult{Avatar: view}

	// Act
	response, payload := h.do(t, http.MethodGet,
		"/api/v1/avatars/"+testAvatar+"/metadata", "", nil)

	// Assert
	require.Equal(t, http.StatusOK, response.StatusCode)

	body := decode[avatarBody](t, payload)
	assert.Nil(t, body.File, "no file yet, and an empty object would read as a real one")
	assert.Empty(t, body.Variants)
	assert.NotContains(t, string(payload), `"file":`)
}

func TestResolveRedirectsAndCaches(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	h.resolve.result = usecase.ResolveResult{
		URL: "https://domain.com/upload/f/100x100.jpg",
	}

	// Act
	response, _ := h.do(t, http.MethodGet,
		"/api/v1/avatars/"+testAvatar+"?size=100x100", "", nil)

	// Assert
	require.Equal(t, http.StatusFound, response.StatusCode)
	assert.Equal(t, "https://domain.com/upload/f/100x100.jpg", response.Header.Get("Location"))
	assert.Equal(t, "public, max-age=300", response.Header.Get("Cache-Control"))
	assert.Equal(t, "100x100", h.resolve.cmd.Size)
}

func TestPlaceholderIsCachedForLess(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	h.current.result = usecase.ResolveResult{
		URL:         "https://domain.com/static/placeholder.png",
		Placeholder: true,
	}

	// Act
	response, _ := h.do(t, http.MethodGet, "/api/v1/users/"+testUser+"/avatar", "", nil)

	// Assert
	require.Equal(t, http.StatusFound, response.StatusCode)
	assert.Equal(t, "public, max-age=30", response.Header.Get("Cache-Control"),
		"a fresh upload must not stay hidden behind a five minute placeholder")
}

func TestListPassesPaginationThrough(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	view := testReadyView(t)
	h.list.result = usecase.ListAvatarsResult{
		Items: []usecase.AvatarView{view},
		URLs: map[string]map[string]string{
			view.ID.String(): {"300x300": "https://domain.com/upload/f/300x300.jpg"},
		},
		NextCursor: "next-page",
	}

	// Act
	response, payload := h.do(t, http.MethodGet,
		"/api/v1/users/"+testUser+"/avatars?limit=7&cursor=abc", "", nil)

	// Assert
	require.Equal(t, http.StatusOK, response.StatusCode, "body: %s", payload)

	body := decode[listBody](t, payload)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "next-page", body.NextCursor)
	assert.Equal(t, "https://domain.com/upload/f/300x300.jpg", body.Items[0].Variants[1].URL)

	assert.Equal(t, 7, h.list.cmd.Limit)
	assert.Equal(t, "abc", h.list.cmd.Cursor)
	assert.Equal(t, testUser, h.list.cmd.UserID.String())
}

func TestListRejectsANonNumericLimit(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for _, limit := range []string{"many", "-1", "1.5"} {
		t.Run(limit, func(t *testing.T) {
			response, payload := h.do(t, http.MethodGet,
				"/api/v1/users/"+testUser+"/avatars?limit="+limit, "", nil)

			require.Equal(t, http.StatusBadRequest, response.StatusCode)
			assert.Equal(t, "invalid_request", decode[errorBody](t, payload).Error)
		})
	}
}

func TestDeleteIsQuietAndIdempotent(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	h.remove.result = usecase.DeleteAvatarResult{Status: domain.AvatarStatusDeleted}

	// Act
	response, payload := h.asUser(t, http.MethodDelete, "/api/v1/avatars/"+testAvatar, "")

	// Assert
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.Empty(t, payload, "204 carries no body, and a json one would be a protocol error")
	assert.Equal(t, testUser, h.remove.cmd.UserID.String())
}

func TestBindEmailStoresTheHash(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)

	email, err := domain.NewEmail("user@example.com")
	require.NoError(t, err)

	h.bind.result = usecase.BindEmailResult{EmailHash: email.Hash()}

	// Act
	response, payload := h.asUser(t, http.MethodPut, "/api/v1/users/"+testUser,
		`{"email":"User@Example.com"}`)

	// Assert
	require.Equal(t, http.StatusOK, response.StatusCode, "body: %s", payload)

	body := decode[bindEmailBody](t, payload)
	assert.Equal(t, email.Hash().String(), body.EmailHash)
	assert.Equal(t, testUser, body.UserID)
	assert.Equal(t, "user@example.com", h.bind.cmd.Email.String(),
		"the email is normalised before it ever reaches the use case")
}

func TestBindEmailRefusesSomebodyElsesProfile(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	response, payload := h.asUser(t, http.MethodPut, "/api/v1/users/user-2",
		`{"email":"user@example.com"}`)

	require.Equal(t, http.StatusForbidden, response.StatusCode)
	assert.Equal(t, "forbidden", decode[errorBody](t, payload).Error)
	assert.Empty(t, h.bind.cmd.UserID.String(), "the use case must not be reached at all")
}

func TestBindEmailRefusesAnEmailItCannotParse(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for _, email := range []string{"", "not-an-email", "a@b", "a b@example.com"} {
		t.Run(email, func(t *testing.T) {
			response, payload := h.asUser(t, http.MethodPut, "/api/v1/users/"+testUser,
				`{"email":"`+email+`"}`)

			require.Equal(t, http.StatusBadRequest, response.StatusCode)
			assert.Equal(t, "invalid_email", decode[errorBody](t, payload).Error)
		})
	}
}

func TestFailuresBecomeTheDocumentedStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		status  int
		reason  string
		details bool
	}{
		{
			name:    "unsupported mime",
			err:     usecase.ErrUnsupportedMimeType,
			status:  http.StatusBadRequest,
			reason:  "invalid_mime_type",
			details: true,
		},
		{
			name:    "too large",
			err:     usecase.ErrFileTooLarge,
			status:  http.StatusRequestEntityTooLarge,
			reason:  "file_too_large",
			details: true,
		},
		{
			name:    "too many avatars",
			err:     usecase.ErrTooManyAvatars,
			status:  http.StatusConflict,
			reason:  "too_many_avatars",
			details: true,
		},
		{
			name:   "somebody else's avatar",
			err:    usecase.ErrForbidden,
			status: http.StatusForbidden,
			reason: "forbidden",
		},
		{
			name:   "upload gone",
			err:    usecase.ErrUploadNotFound,
			status: http.StatusNotFound,
			reason: "upload_not_found",
		},
		{
			name:    "upload unusable",
			err:     usecase.ErrUploadNotUsable,
			status:  http.StatusConflict,
			reason:  "upload_expired",
			details: true,
		},
		{
			name:   "file is down",
			err:    usecase.ErrFileServiceUnavailable,
			status: http.StatusServiceUnavailable,
			reason: "file_unavailable",
		},
		{
			name:   "file is slow",
			err:    usecase.ErrFileServiceTimeout,
			status: http.StatusGatewayTimeout,
			reason: "timeout",
		},
		{
			name:   "our own deadline",
			err:    context.DeadlineExceeded,
			status: http.StatusGatewayTimeout,
			reason: "timeout",
		},
		{
			name:   "concurrent write",
			err:    usecase.ErrStaleVersion,
			status: http.StatusConflict,
			reason: "conflict",
		},
		{
			name:   "anything we did not plan for",
			err:    errors.New("connection reset by peer"),
			status: http.StatusInternalServerError,
			reason: "internal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			h := newHarness(t)
			h.start.err = test.err

			// Act
			response, payload := h.asUser(t, http.MethodPost,
				"/api/v1/avatars/uploads", `{"mime_type":"image/jpeg"}`)

			// Assert
			require.Equal(t, test.status, response.StatusCode, "body: %s", payload)

			body := decode[errorBody](t, payload)
			assert.Equal(t, test.reason, body.Error)

			if test.details {
				assert.NotEmpty(t, body.Details, "the client can act on this one")
			} else {
				assert.Empty(t, body.Details,
					"a 403, a 404 or a 500 must not describe what went on inside")
			}
		})
	}
}

func TestReadFailuresBecomeTheDocumentedStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{
			name:   "not ready yet",
			err:    usecase.ErrAvatarNotReady,
			status: http.StatusConflict,
			reason: "avatar_not_ready",
		},
		{
			name:   "no such size",
			err:    usecase.ErrVariantAbsent,
			status: http.StatusNotFound,
			reason: "variant_not_found",
		},
		{
			name:   "no such avatar",
			err:    domain.ErrAvatarNotFound,
			status: http.StatusNotFound,
			reason: "avatar_not_found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.resolve.err = test.err

			response, payload := h.do(t, http.MethodGet, "/api/v1/avatars/"+testAvatar, "", nil)

			require.Equal(t, test.status, response.StatusCode)
			assert.Equal(t, test.reason, decode[errorBody](t, payload).Error)
		})
	}
}

func TestAPanicBecomesAFiveHundred(t *testing.T) {
	t.Parallel()

	// Arrange
	h := newHarness(t)
	h.get.panics = true

	// Act
	response, payload := h.do(t, http.MethodGet,
		"/api/v1/avatars/"+testAvatar+"/metadata", "", nil)

	// Assert
	require.Equal(t, http.StatusInternalServerError, response.StatusCode)
	assert.Equal(t, "internal", decode[errorBody](t, payload).Error,
		"without recovery the client gets a dropped connection and no status at all")
}

func TestUnknownRouteIsNotOurs(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	response, _ := h.do(t, http.MethodGet, "/api/v1/nothing", "", nil)
	assert.Equal(t, http.StatusNotFound, response.StatusCode)

	response, _ = h.do(t, http.MethodPatch, "/api/v1/avatars/"+testAvatar, "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, response.StatusCode,
		"the mux knows the path, so the method is what is wrong")
}

func TestNewHandlerRefusesAnIncompleteSet(t *testing.T) {
	t.Parallel()

	full := Handlers{
		StartUpload:          &stubStartUpload{},
		CompleteUpload:       &stubCompleteUpload{},
		GetAvatar:            &stubGetAvatar{},
		ListAvatars:          &stubListAvatars{},
		ResolveAvatar:        &stubResolveAvatar{},
		ResolveCurrentAvatar: &stubResolveCurrent{},
		ResolveByEmail:       &stubResolveByEmail{},
		DeleteAvatar:         &stubDeleteAvatar{},
		BindEmail:            &stubBindEmail{},
	}

	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name   string
		mutate func(*Handlers)
	}{
		{name: "start upload", mutate: func(h *Handlers) { h.StartUpload = nil }},
		{name: "complete upload", mutate: func(h *Handlers) { h.CompleteUpload = nil }},
		{name: "get avatar", mutate: func(h *Handlers) { h.GetAvatar = nil }},
		{name: "list avatars", mutate: func(h *Handlers) { h.ListAvatars = nil }},
		{name: "resolve avatar", mutate: func(h *Handlers) { h.ResolveAvatar = nil }},
		{name: "resolve current", mutate: func(h *Handlers) { h.ResolveCurrentAvatar = nil }},
		{name: "resolve by email", mutate: func(h *Handlers) { h.ResolveByEmail = nil }},
		{name: "delete avatar", mutate: func(h *Handlers) { h.DeleteAvatar = nil }},
		{name: "bind email", mutate: func(h *Handlers) { h.BindEmail = nil }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			handlers := full
			test.mutate(&handlers)

			_, err := NewHandler(handlers, Config{Timeout: time.Second}, logger)
			require.ErrorIs(t, err, ErrIncompleteHandler)
		})
	}

	_, err := NewHandler(full, Config{}, logger)
	require.ErrorIs(t, err, ErrIncompleteHandler, "a route without a deadline is a hung request")

	_, err = NewHandler(full, Config{Timeout: time.Second}, nil)
	require.ErrorIs(t, err, ErrIncompleteHandler)

	_, err = NewHandler(full, Config{Timeout: time.Second}, logger)
	require.NoError(t, err)
}
