package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

var ErrInvalidRegistry = errors.New("invalid health registry")

const (
	statusOK       = "ok"
	statusDegraded = "degraded"

	RouteLive  = "GET /health/live"
	RouteReady = "GET /health/ready"
)

type Check struct {
	Name     string
	Critical bool
	Probe    func(ctx context.Context) error
}

type Config struct {
	Timeout time.Duration `env:"PROBE_TIMEOUT" envDefault:"2s"`
}

type Registry struct {
	checks  []Check
	timeout time.Duration
}

type checkReport struct {
	Status   string `json:"status"`
	Critical bool   `json:"critical"`
	Error    string `json:"error,omitempty"`
}

type report struct {
	Status string                 `json:"status"`
	Checks map[string]checkReport `json:"checks"`
}

func New(cfg Config, checks ...Check) (*Registry, error) {
	if cfg.Timeout <= 0 {
		return nil, fmt.Errorf("%w: timeout must be positive", ErrInvalidRegistry)
	}

	seen := make(map[string]struct{}, len(checks))

	for _, check := range checks {
		switch {
		case check.Name == "":
			return nil, fmt.Errorf("%w: a check needs a name", ErrInvalidRegistry)
		case check.Probe == nil:
			return nil, fmt.Errorf("%w: check %q has no probe", ErrInvalidRegistry, check.Name)
		}

		if _, duplicate := seen[check.Name]; duplicate {
			return nil, fmt.Errorf("%w: check %q is declared twice", ErrInvalidRegistry, check.Name)
		}

		seen[check.Name] = struct{}{}
	}

	return &Registry{checks: checks, timeout: cfg.Timeout}, nil
}

func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(RouteLive, r.live)
	mux.HandleFunc(RouteReady, r.ready)

	return mux
}

func (r *Registry) live(w http.ResponseWriter, _ *http.Request) {
	write(w, http.StatusOK, report{Status: statusOK, Checks: map[string]checkReport{}})
}

func (r *Registry) Healthy(ctx context.Context) bool {
	healthy, _ := r.evaluate(ctx)

	return healthy
}

func (r *Registry) evaluate(ctx context.Context) (bool, report) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	results := make([]checkReport, len(r.checks))

	var group sync.WaitGroup

	for i, check := range r.checks {
		group.Add(1)

		go func() {
			defer group.Done()

			result := checkReport{Status: statusOK, Critical: check.Critical}

			if err := check.Probe(ctx); err != nil {
				result.Status = statusDegraded
				result.Error = err.Error()
			}

			results[i] = result
		}()
	}

	group.Wait()

	answer := report{Status: statusOK, Checks: make(map[string]checkReport, len(r.checks))}
	healthy := true

	for i, check := range r.checks {
		answer.Checks[check.Name] = results[i]

		if results[i].Status != statusOK && check.Critical {
			answer.Status = statusDegraded
			healthy = false
		}
	}

	return healthy, answer
}

func (r *Registry) ready(w http.ResponseWriter, request *http.Request) {
	healthy, answer := r.evaluate(request.Context())

	status := http.StatusOK
	if !healthy {
		status = http.StatusServiceUnavailable
	}

	write(w, status, answer)
}

func write(w http.ResponseWriter, status int, body report) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(body)
}
