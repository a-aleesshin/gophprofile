//go:build integration

package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func waitReady(t *testing.T, addr string) {
	t.Helper()

	var body struct {
		Status string `json:"status"`
	}

	require.Eventually(t, func() bool {
		response, err := http.Get("http://" + addr + "/health/ready")
		if err != nil {
			return false
		}

		defer func() { _ = response.Body.Close() }()

		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			return false
		}

		return response.StatusCode == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)

	assert.Equal(t, "ok", body.Status)
}

func TestBuildServerStartsAndAnswers(t *testing.T) {
	// Arrange
	cfg := serverConfig(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())

	runner, shutdown, err := BuildServer(ctx, cfg, logger)
	require.NoError(t, err,
		"the server must assemble without a broker and without file being up")

	defer shutdown()

	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	waitReady(t, cfg.HealthServer.Addr)

	t.Run("metadata answers from the database", func(t *testing.T) {
		response, err := http.Get("http://" + cfg.HTTP.Addr +
			"/api/v1/avatars/0193abcd-ef01-7a23-8b45-0123456789ab/metadata")
		require.NoError(t, err)

		defer func() { _ = response.Body.Close() }()

		payload, err := io.ReadAll(response.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusNotFound, response.StatusCode)
		assert.Contains(t, string(payload), "avatar_not_found",
			"an unknown avatar is a 404, not an internal error")
	})

	t.Run("a public resolve falls back to the placeholder", func(t *testing.T) {
		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}

		response, err := client.Get("http://" + cfg.HTTP.Addr +
			"/api/v1/users/nobody/avatar")
		require.NoError(t, err)

		defer func() { _ = response.Body.Close() }()

		assert.Equal(t, http.StatusFound, response.StatusCode)
		assert.Equal(t, cfg.URLs.Placeholder, response.Header.Get("Location"))
	})

	t.Run("an upload needs a user", func(t *testing.T) {
		response, err := http.Post("http://"+cfg.HTTP.Addr+"/api/v1/avatars/uploads",
			"application/json", strings.NewReader(`{"mime_type":"image/jpeg"}`))
		require.NoError(t, err)

		defer func() { _ = response.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	})

	t.Run("an upload gives up when file is unreachable", func(t *testing.T) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
			"http://"+cfg.HTTP.Addr+"/api/v1/avatars/uploads",
			strings.NewReader(`{"file_name":"a.jpg","mime_type":"image/jpeg","size_bytes":2048}`))
		require.NoError(t, err)

		request.Header.Set("X-User-ID", "user-1")
		request.Header.Set("Content-Type", "application/json")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)

		defer func() { _ = response.Body.Close() }()

		assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode,
			"nobody listens on the configured file address, and that is a 503, not a 500")
	})

	// Act
	cancel()

	// Assert
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled,
			"cancelling the context must stop every runner")
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not stop after the context was cancelled")
	}
}

func TestBuildServerRunsWithoutPprof(t *testing.T) {
	// Arrange
	cfg := serverConfig(t)

	// The port the config would have used, kept so the assertion can prove that
	// nothing listens there — comparing against an empty address would pass
	// whatever the code does.
	absent := cfg.PprofServer.Addr
	cfg.PprofServer.Addr = ""

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Act
	runner, shutdown, err := BuildServer(ctx, cfg, logger)

	// Assert
	require.NoError(t, err,
		"profiling is opt in, and an unset address must not stop the service")

	defer shutdown()

	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	waitReady(t, cfg.HealthServer.Addr)

	response, err := http.Get("http://" + absent + "/debug/pprof/")
	if err == nil {
		defer func() { _ = response.Body.Close() }()
	}

	require.Error(t, err, "nothing may be listening where pprof would have been")

	cancel()
	<-done
}
