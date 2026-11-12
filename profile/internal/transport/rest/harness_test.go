package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const (
	testUser   = "user-1"
	testAvatar = "0193abcd-ef01-7a23-8b45-0123456789ab"
)

var testNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

type stubStartUpload struct {
	cmd      usecase.StartUploadCommand
	result   usecase.StartUploadResult
	err      error
	deadline bool
}

func (s *stubStartUpload) Handle(
	ctx context.Context,
	cmd usecase.StartUploadCommand,
) (usecase.StartUploadResult, error) {
	s.cmd = cmd
	_, s.deadline = ctx.Deadline()

	return s.result, s.err
}

type stubCompleteUpload struct {
	cmd    usecase.CompleteUploadCommand
	result usecase.CompleteUploadResult
	err    error
}

func (s *stubCompleteUpload) Handle(
	_ context.Context,
	cmd usecase.CompleteUploadCommand,
) (usecase.CompleteUploadResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubGetAvatar struct {
	cmd    usecase.GetAvatarCommand
	result usecase.GetAvatarResult
	err    error
	panics bool
}

func (s *stubGetAvatar) Handle(
	_ context.Context,
	cmd usecase.GetAvatarCommand,
) (usecase.GetAvatarResult, error) {
	s.cmd = cmd

	if s.panics {
		panic("the projection blew up")
	}

	return s.result, s.err
}

type stubListAvatars struct {
	cmd    usecase.ListAvatarsCommand
	result usecase.ListAvatarsResult
	err    error
}

func (s *stubListAvatars) Handle(
	_ context.Context,
	cmd usecase.ListAvatarsCommand,
) (usecase.ListAvatarsResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubResolveAvatar struct {
	cmd    usecase.ResolveAvatarCommand
	result usecase.ResolveResult
	err    error
}

func (s *stubResolveAvatar) Handle(
	_ context.Context,
	cmd usecase.ResolveAvatarCommand,
) (usecase.ResolveResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubResolveCurrent struct {
	cmd    usecase.ResolveCurrentAvatarCommand
	result usecase.ResolveResult
	err    error
}

func (s *stubResolveCurrent) Handle(
	_ context.Context,
	cmd usecase.ResolveCurrentAvatarCommand,
) (usecase.ResolveResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubResolveByEmail struct {
	cmd    usecase.ResolveByEmailCommand
	result usecase.ResolveResult
	err    error
}

func (s *stubResolveByEmail) Handle(
	_ context.Context,
	cmd usecase.ResolveByEmailCommand,
) (usecase.ResolveResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubDeleteAvatar struct {
	cmd    usecase.DeleteAvatarCommand
	result usecase.DeleteAvatarResult
	err    error
}

func (s *stubDeleteAvatar) Handle(
	_ context.Context,
	cmd usecase.DeleteAvatarCommand,
) (usecase.DeleteAvatarResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type stubBindEmail struct {
	cmd    usecase.BindEmailCommand
	result usecase.BindEmailResult
	err    error
}

func (s *stubBindEmail) Handle(
	_ context.Context,
	cmd usecase.BindEmailCommand,
) (usecase.BindEmailResult, error) {
	s.cmd = cmd

	return s.result, s.err
}

type harness struct {
	server *httptest.Server

	start    *stubStartUpload
	complete *stubCompleteUpload
	get      *stubGetAvatar
	list     *stubListAvatars
	resolve  *stubResolveAvatar
	current  *stubResolveCurrent
	byEmail  *stubResolveByEmail
	remove   *stubDeleteAvatar
	bind     *stubBindEmail
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		start:    &stubStartUpload{},
		complete: &stubCompleteUpload{},
		get:      &stubGetAvatar{},
		list:     &stubListAvatars{},
		resolve:  &stubResolveAvatar{},
		current:  &stubResolveCurrent{},
		byEmail:  &stubResolveByEmail{},
		remove:   &stubDeleteAvatar{},
		bind:     &stubBindEmail{},
	}

	handler, err := NewHandler(Handlers{
		StartUpload:          h.start,
		CompleteUpload:       h.complete,
		GetAvatar:            h.get,
		ListAvatars:          h.list,
		ResolveAvatar:        h.resolve,
		ResolveCurrentAvatar: h.current,
		ResolveByEmail:       h.byEmail,
		DeleteAvatar:         h.remove,
		BindEmail:            h.bind,
	}, Config{Timeout: time.Second},
		slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	mux := http.NewServeMux()
	handler.Register(mux)

	h.server = httptest.NewServer(mux)
	t.Cleanup(h.server.Close)

	return h
}

func (h *harness) do(
	t *testing.T, method, path, body string, headers map[string]string,
) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	request, err := http.NewRequestWithContext(t.Context(), method, h.server.URL+path, reader)
	require.NoError(t, err)

	for name, value := range headers {
		request.Header.Set(name, value)
	}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	response, err := client.Do(request)
	require.NoError(t, err)

	defer func() { require.NoError(t, response.Body.Close()) }()

	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return response, payload
}

func (h *harness) asUser(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()

	return h.do(t, method, path, body, map[string]string{HeaderUserID: testUser})
}

func testAvatarID(t *testing.T) domain.AvatarID {
	t.Helper()

	value, err := domain.ParseAvatarID(testAvatar)
	require.NoError(t, err)

	return value
}

func testUserID(t *testing.T) domain.UserID {
	t.Helper()

	value, err := domain.NewUserID(testUser)
	require.NoError(t, err)

	return value
}

func testFileRef(t *testing.T) domain.FileRef {
	t.Helper()

	value, err := domain.ParseFileRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testReadyView(t *testing.T) usecase.AvatarView {
	t.Helper()

	return usecase.AvatarView{
		ID:        testAvatarID(t),
		UserID:    testUserID(t),
		FileID:    testFileRef(t),
		Status:    domain.AvatarStatusReady,
		FileName:  "avatar.jpg",
		MimeType:  "image/jpeg",
		SizeBytes: 2048,
		Width:     1920,
		Height:    1080,
		Variants: []usecase.VariantView{
			{Name: "100x100", Ext: "jpg", MimeType: "image/jpeg", Width: 100, Height: 100},
			{Name: "300x300", Ext: "jpg", MimeType: "image/jpeg", Width: 300, Height: 300},
		},
		CreatedAt: testNow,
		UpdatedAt: testNow,
	}
}

func decode[T any](t *testing.T, payload []byte) T {
	t.Helper()

	var value T

	require.NoError(t, json.Unmarshal(payload, &value), "body: %s", payload)

	return value
}
