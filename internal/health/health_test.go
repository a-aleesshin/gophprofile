package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func okProbe(context.Context) error { return nil }

func failingProbe(context.Context) error { return errors.New("connection refused") }

func ask(t *testing.T, registry *Registry, path string) (*httptest.ResponseRecorder, report) {
	t.Helper()

	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

	var body report
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))

	return recorder, body
}

func testRegistry(t *testing.T, checks ...Check) *Registry {
	t.Helper()

	registry, err := New(Config{Timeout: time.Second}, checks...)
	require.NoError(t, err)

	return registry
}

func TestLiveAnswersWhileTheProcessRuns(t *testing.T) {
	t.Parallel()

	// Arrange
	registry := testRegistry(t, Check{Name: "postgres", Critical: true, Probe: failingProbe})

	// Act
	recorder, body := ask(t, registry, "/health/live")

	// Assert
	assert.Equal(t, http.StatusOK, recorder.Code,
		"liveness says the process is up, not that its dependencies are")
	assert.Equal(t, statusOK, body.Status)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
}

func TestReadyIsHealthyWhenEverythingAnswers(t *testing.T) {
	t.Parallel()

	// Arrange
	registry := testRegistry(t,
		Check{Name: "postgres", Critical: true, Probe: okProbe},
		Check{Name: "kafka", Probe: okProbe},
	)

	// Act
	recorder, body := ask(t, registry, "/health/ready")

	// Assert
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, statusOK, body.Status)
	assert.Equal(t, statusOK, body.Checks["postgres"].Status)
	assert.True(t, body.Checks["postgres"].Critical)
	assert.False(t, body.Checks["kafka"].Critical)
}

func TestReadyFailsOnlyForACriticalDependency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		critical bool
		want     int
	}{
		{name: "critical dependency is down", critical: true, want: http.StatusServiceUnavailable},
		{name: "optional dependency is down", critical: false, want: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Arrange
			registry := testRegistry(t,
				Check{Name: "postgres", Critical: true, Probe: okProbe},
				Check{Name: "broker", Critical: test.critical, Probe: failingProbe},
			)

			// Act
			recorder, body := ask(t, registry, "/health/ready")

			// Assert
			assert.Equal(t, test.want, recorder.Code)
			assert.Equal(t, statusDegraded, body.Checks["broker"].Status,
				"a degraded dependency is reported either way")
			assert.Equal(t, "connection refused", body.Checks["broker"].Error)
		})
	}
}

func TestReadyRunsProbesInParallel(t *testing.T) {
	t.Parallel()

	// Arrange
	slow := func(context.Context) error {
		time.Sleep(150 * time.Millisecond)

		return nil
	}

	registry := testRegistry(t,
		Check{Name: "one", Probe: slow},
		Check{Name: "two", Probe: slow},
		Check{Name: "three", Probe: slow},
	)

	// Act
	started := time.Now()
	recorder, _ := ask(t, registry, "/health/ready")
	elapsed := time.Since(started)

	// Assert
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Less(t, elapsed, 400*time.Millisecond,
		"three probes of 150ms must not add up, or the probe times out on its own")
}

func TestReadyStopsProbesAtTheTimeout(t *testing.T) {
	t.Parallel()

	// Arrange
	registry, err := New(Config{Timeout: 50 * time.Millisecond},
		Check{Name: "postgres", Critical: true, Probe: func(ctx context.Context) error {
			<-ctx.Done()

			return ctx.Err()
		}})
	require.NoError(t, err)

	// Act
	recorder, body := ask(t, registry, "/health/ready")

	// Assert
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, body.Checks["postgres"].Error, "deadline")
}

func TestNewRejectsABrokenRegistry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout time.Duration
		checks  []Check
	}{
		{name: "no timeout", checks: []Check{{Name: "db", Probe: okProbe}}},
		{
			name: "check without a name", timeout: time.Second,
			checks: []Check{{Probe: okProbe}},
		},
		{
			name: "check without a probe", timeout: time.Second,
			checks: []Check{{Name: "db"}},
		},
		{
			name: "the same name twice", timeout: time.Second,
			checks: []Check{{Name: "db", Probe: okProbe}, {Name: "db", Probe: okProbe}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Act
			registry, err := New(Config{Timeout: test.timeout}, test.checks...)

			// Assert
			require.ErrorIs(t, err, ErrInvalidRegistry)
			assert.Nil(t, registry)
		})
	}
}
