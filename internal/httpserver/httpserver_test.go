package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freeAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

func testConfig(addr string) Config {
	return Config{
		Addr:            addr,
		ReadTimeout:     time.Second,
		WriteTimeout:    time.Second,
		IdleTimeout:     time.Second,
		ShutdownTimeout: time.Second,
	}
}

func TestServerAnswersAndStopsOnContext(t *testing.T) {
	t.Parallel()

	// Arrange
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("alive"))
	})

	addr := freeAddr(t)

	server, err := New("health", handler, testConfig(addr), discard())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- server.Run(ctx) }()

	// Act
	var body []byte

	require.Eventually(t, func() bool {
		response, getErr := http.Get("http://" + addr)
		if getErr != nil {
			return false
		}

		defer func() { _ = response.Body.Close() }()

		body, _ = io.ReadAll(response.Body)

		return response.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond)

	cancel()

	// Assert
	assert.Equal(t, "alive", string(body))

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not stop after the context was cancelled")
	}

	_, err = http.Get("http://" + addr)
	assert.Error(t, err, "the port must be released after shutdown")
}

func TestServerReportsAPortItCannotTake(t *testing.T) {
	t.Parallel()

	// Arrange
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = listener.Close() })

	server, err := New("metrics", http.NotFoundHandler(),
		testConfig(listener.Addr().String()), discard())
	require.NoError(t, err)

	// Act
	runErr := server.Run(t.Context())

	// Assert
	require.Error(t, runErr)
	assert.ErrorContains(t, runErr, "metrics",
		"the failing server must name itself, there are three of them")
}

func TestNewRejectsAnIncompleteServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		server  string
		handler http.Handler
		cfg     Config
		logger  *slog.Logger
	}{
		{name: "no name", handler: http.NotFoundHandler(), cfg: testConfig(":0"), logger: discard()},
		{name: "no handler", server: "health", cfg: testConfig(":0"), logger: discard()},
		{
			name: "no address", server: "health", handler: http.NotFoundHandler(),
			cfg: Config{ShutdownTimeout: time.Second}, logger: discard(),
		},
		{
			name: "no shutdown timeout", server: "health", handler: http.NotFoundHandler(),
			cfg: Config{Addr: ":0"}, logger: discard(),
		},
		{
			name: "no logger", server: "health", handler: http.NotFoundHandler(),
			cfg: testConfig(":0"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			server, err := New(test.server, test.handler, test.cfg, test.logger)

			// Assert
			require.ErrorIs(t, err, ErrInvalidConfig)
			assert.Nil(t, server)
		})
	}
}

func TestConfigReportsWhenASectionIsUnset(t *testing.T) {
	t.Parallel()

	assert.True(t, Config{}.Disabled())
	assert.True(t, Config{ReadTimeout: time.Second}.Disabled(),
		"timeouts without an address still mean the section was never configured")
	assert.False(t, Config{Addr: "127.0.0.1:6060"}.Disabled())
}

func TestNewStillRefusesAnEmptyAddress(t *testing.T) {
	t.Parallel()

	_, err := New("public", http.NotFoundHandler(),
		Config{ShutdownTimeout: time.Second}, slog.New(slog.DiscardHandler))

	require.ErrorIs(t, err, ErrInvalidConfig)
	assert.ErrorContains(t, err, "public",
		"the message has to name the section, because the env tag no longer does")
}
