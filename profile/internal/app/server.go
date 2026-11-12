package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/internal/worker"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/filegrpc"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/messaging"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/postgres"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/urls"
	"github.com/a-aleesshin/gophprofile/profile/internal/config"
	"github.com/a-aleesshin/gophprofile/profile/internal/transport/rest"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

type Closer func()

func BuildServer(
	ctx context.Context,
	cfg config.Server,
	logger *slog.Logger,
) (*worker.Worker, Closer, error) {
	var closers []func()

	shutdown := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	fail := func(err error) (*worker.Worker, Closer, error) {
		shutdown()

		return nil, nil, err
	}

	pool, err := pgtx.Connect(ctx, cfg.Postgres)
	if err != nil {
		return fail(fmt.Errorf("connect to postgres: %w", err))
	}

	closers = append(closers, pool.Close)

	db := pgtx.New(pool)

	policies, err := policy.New(cfg.Policy)
	if err != nil {
		return fail(fmt.Errorf("build upload policy: %w", err))
	}

	links, err := urls.New(cfg.URLs)
	if err != nil {
		return fail(fmt.Errorf("build url builder: %w", err))
	}

	files, err := filegrpc.New(cfg.File)
	if err != nil {
		return fail(fmt.Errorf("open file client: %w", err))
	}

	closers = append(closers, func() { _ = files.Close() })

	now := clock.System{}

	profiles := postgres.NewProfiles(db)
	avatars := postgres.NewAvatars(db)
	publisher := messaging.NewPublisher(outbox.NewWriter(db, now), db)

	handlers := rest.Handlers{
		StartUpload: usecase.NewStartUploadHandler(
			policies, profiles, avatars, files, db, now),
		CompleteUpload:       usecase.NewCompleteUploadHandler(avatars, files, db, now),
		GetAvatar:            usecase.NewGetAvatarHandler(avatars, links),
		ListAvatars:          usecase.NewListAvatarsHandler(avatars, links),
		ResolveAvatar:        usecase.NewResolveAvatarHandler(avatars, links),
		ResolveCurrentAvatar: usecase.NewResolveCurrentAvatarHandler(currentAvatarSource{profiles, avatars}, links),
		ResolveByEmail:       usecase.NewResolveByEmailHandler(byEmailSource{profiles, avatars}, links),
		DeleteAvatar: usecase.NewDeleteAvatarHandler(
			avatars, profiles, publisher, db, now),
		BindEmail: usecase.NewBindEmailHandler(profiles, db, now),
	}

	api, err := rest.NewHandler(handlers, cfg.REST, logger)
	if err != nil {
		return fail(fmt.Errorf("build rest handler: %w", err))
	}

	mux := http.NewServeMux()
	api.Register(mux)

	checks, err := health.New(cfg.Health,
		health.Check{Name: "postgres", Critical: true, Probe: pool.Ping},
	)
	if err != nil {
		return fail(fmt.Errorf("build health registry: %w", err))
	}

	publicServer, err := httpserver.New("public", mux, cfg.HTTP, logger)
	if err != nil {
		return fail(fmt.Errorf("build public server: %w", err))
	}

	healthServer, err := httpserver.New("health", checks.Handler(), cfg.HealthServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build health server: %w", err))
	}

	metricsServer, err := httpserver.New("metrics", metricsHandler(), cfg.MetricsServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build metrics server: %w", err))
	}

	pprofServer, err := httpserver.New("pprof", pprofHandler(), cfg.PprofServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build pprof server: %w", err))
	}

	runner := worker.New().
		Add("public server", publicServer).
		Add("health server", healthServer).
		Add("metrics server", metricsServer).
		Add("pprof server", pprofServer)

	return runner, shutdown, nil
}

// The two resolvers need a profile and a projection at once, and the
// repositories are split by aggregate, so the pair is composed here rather
// than widening either repository.
type currentAvatarSource struct {
	*postgres.Profiles
	*postgres.Avatars
}

type byEmailSource struct {
	*postgres.Profiles
	*postgres.Avatars
}
