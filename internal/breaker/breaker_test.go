package breaker

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stubClock struct {
	now time.Time
}

func (c *stubClock) Now() time.Time { return c.now }

func (c *stubClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestBreaker(t *testing.T, cfg Config) (*Breaker, *stubClock) {
	t.Helper()

	clk := &stubClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	b, err := New(t.Name(), cfg, clk)
	if err != nil {
		t.Fatalf("new breaker: %v", err)
	}

	return b, clk
}

func defaults() Config {
	return Config{Enabled: true, Failures: 3, Cooldown: 10 * time.Second, Probes: 1}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	clk := &stubClock{}

	cases := map[string]struct {
		name string
		cfg  Config
		clk  Clock
	}{
		"empty name":    {name: "", cfg: defaults(), clk: clk},
		"nil clock":     {name: "dep", cfg: defaults(), clk: nil},
		"zero failures": {name: "dep", cfg: Config{Enabled: true, Failures: 0, Cooldown: time.Second, Probes: 1}, clk: clk},
		"zero cooldown": {name: "dep", cfg: Config{Enabled: true, Failures: 1, Cooldown: 0, Probes: 1}, clk: clk},
		"zero probes":   {name: "dep", cfg: Config{Enabled: true, Failures: 1, Cooldown: time.Second, Probes: 0}, clk: clk},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(tc.name, tc.cfg, tc.clk); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestDisabledBreakerAllowsEverything(t *testing.T) {
	cfg := defaults()
	cfg.Enabled = false

	b, err := New("dep", cfg, &stubClock{})
	if err != nil {
		t.Fatalf("new breaker: %v", err)
	}

	if b != nil {
		t.Fatalf("want a nil breaker when disabled")
	}

	for range 100 {
		if err := b.Allow(); err != nil {
			t.Fatalf("nil breaker must allow: %v", err)
		}

		b.Record(true)
	}

	if b.State() != StateClosed {
		t.Fatalf("nil breaker must report closed")
	}
}

func TestOpensAfterConsecutiveFailures(t *testing.T) {
	b, _ := newTestBreaker(t, defaults())

	for i := range 3 {
		if err := b.Allow(); err != nil {
			t.Fatalf("call %d must be allowed: %v", i, err)
		}

		b.Record(true)
	}

	if got := b.State(); got != StateOpen {
		t.Fatalf("want open after 3 failures, got %s", got)
	}

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
}

func TestSuccessResetsTheStrikeCount(t *testing.T) {
	b, _ := newTestBreaker(t, defaults())

	for range 2 {
		_ = b.Allow()
		b.Record(true)
	}

	_ = b.Allow()
	b.Record(false)

	for range 2 {
		_ = b.Allow()
		b.Record(true)
	}

	if got := b.State(); got != StateClosed {
		t.Fatalf("want still closed, got %s", got)
	}
}

func TestHalfOpenProbeClosesOnSuccess(t *testing.T) {
	b, clk := newTestBreaker(t, defaults())

	for range 3 {
		_ = b.Allow()
		b.Record(true)
	}

	clk.advance(11 * time.Second)

	if err := b.Allow(); err != nil {
		t.Fatalf("probe must be allowed after the cooldown: %v", err)
	}

	if got := b.State(); got != StateHalfOpen {
		t.Fatalf("want half-open, got %s", got)
	}

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("second probe must be refused, got %v", err)
	}

	b.Record(false)

	if got := b.State(); got != StateClosed {
		t.Fatalf("want closed after a successful probe, got %s", got)
	}
}

func TestHalfOpenProbeReopensOnFailure(t *testing.T) {
	b, clk := newTestBreaker(t, defaults())

	for range 3 {
		_ = b.Allow()
		b.Record(true)
	}

	clk.advance(11 * time.Second)
	_ = b.Allow()
	b.Record(true)

	if got := b.State(); got != StateOpen {
		t.Fatalf("want open again, got %s", got)
	}

	if err := b.Allow(); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen right after reopening, got %v", err)
	}

	clk.advance(11 * time.Second)

	if err := b.Allow(); err != nil {
		t.Fatalf("cooldown must restart from the reopening: %v", err)
	}
}

func TestDoReportsTheCallResult(t *testing.T) {
	b, _ := newTestBreaker(t, defaults())

	boom := errors.New("boom")
	failed := func(err error) bool { return err != nil }

	for range 3 {
		if err := b.Do(func() error { return boom }, failed); !errors.Is(err, boom) {
			t.Fatalf("want the call error back, got %v", err)
		}
	}

	called := false

	err := b.Do(func() error {
		called = true

		return nil
	}, failed)

	if !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}

	if called {
		t.Fatalf("an open breaker must not run the call")
	}
}

func TestTransportOpensOnServerErrors(t *testing.T) {
	status := http.StatusInternalServerError

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer server.Close()

	b, _ := newTestBreaker(t, defaults())
	client := &http.Client{Transport: NewTransport(nil, b)}

	for range 3 {
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		_ = response.Body.Close()
	}

	if got := b.State(); got != StateOpen {
		t.Fatalf("want open after three 500s, got %s", got)
	}

	if _, err := client.Get(server.URL); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
}

func TestTransportIgnoresClientErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	b, _ := newTestBreaker(t, defaults())
	client := &http.Client{Transport: NewTransport(nil, b)}

	for range 10 {
		response, err := client.Get(server.URL)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		_ = response.Body.Close()
	}

	if got := b.State(); got != StateClosed {
		t.Fatalf("a missing object is not a broken dependency, got %s", got)
	}
}
