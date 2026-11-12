package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	filev1 "github.com/a-aleesshin/gophprofile/file/gen/file/v1"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/link"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/messaging"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/postgres"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/file/internal/config"
	filegrpc "github.com/a-aleesshin/gophprofile/file/internal/transport/grpc"
	"github.com/a-aleesshin/gophprofile/file/internal/transport/serving"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/grpcserver"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/internal/worker"
)

const grpcHealthInterval = 5 * time.Second

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

	store, err := s3.New(cfg.S3, db)
	if err != nil {
		return fail(fmt.Errorf("open object storage: %w", err))
	}

	policies, err := policy.Load(cfg.Policies.Path)
	if err != nil {
		return fail(fmt.Errorf("load policies: %w", err))
	}

	now := clock.System{}

	links, err := link.NewBuilder(cfg.Links, now)
	if err != nil {
		return fail(fmt.Errorf("build link builder: %w", err))
	}

	uploads := postgres.NewUploads(db)
	files := postgres.NewFiles(db)
	variants := postgres.NewVariants(db)
	publisher := messaging.NewPublisher(outbox.NewWriter(db, now))

	handlers := filegrpc.Handlers{
		CreateUpload: usecase.NewCreateUploadHandler(policies, uploads, uploads, store, now),
		CompleteUpload: usecase.NewCompleteUploadHandler(
			policies, uploads, files, store, publisher, db, now),
		GetFile:          usecase.NewGetFileHandler(files, variants),
		GetFiles:         usecase.NewGetFilesHandler(files, variants),
		CreateAccessLink: usecase.NewCreateAccessLinkHandler(policies, variants, links, now),
		DeleteFile:       usecase.NewDeleteFileHandler(files, variants, publisher, db, now),
	}

	fileService, err := filegrpc.NewServer(handlers)
	if err != nil {
		return fail(fmt.Errorf("build grpc service: %w", err))
	}

	checks, err := health.New(cfg.Health,
		health.Check{Name: "postgres", Critical: true, Probe: pool.Ping},
		health.Check{Name: "s3", Critical: true, Probe: store.Ping},
	)
	if err != nil {
		return fail(fmt.Errorf("build health registry: %w", err))
	}

	grpcHealth := grpchealth.NewServer()

	grpcServer, err := grpcserver.New(cfg.GRPC, logger, func(server *grpc.Server) {
		filev1.RegisterFileServiceServer(server, fileService)
		healthpb.RegisterHealthServer(server, grpcHealth)
		reflection.Register(server)
	})
	if err != nil {
		return fail(fmt.Errorf("build grpc server: %w", err))
	}

	serveVariant := usecase.NewServeVariantHandler(policies, variants, store)

	content, err := serving.NewHandler(serveVariant, links, cfg.Serving, logger)
	if err != nil {
		return fail(fmt.Errorf("build serving handler: %w", err))
	}

	mux := http.NewServeMux()
	content.Register(mux)

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

	grpcHealthJob, err := worker.Every("grpc-health", grpcHealthInterval, logger,
		func(ctx context.Context) error {
			status := healthpb.HealthCheckResponse_SERVING
			if !checks.Healthy(ctx) {
				status = healthpb.HealthCheckResponse_NOT_SERVING
			}

			grpcHealth.SetServingStatus("", status)
			grpcHealth.SetServingStatus(filev1.FileService_ServiceDesc.ServiceName, status)

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build grpc health job: %w", err))
	}

	runner := worker.New().
		Add("grpc server", grpcServer).
		Add("public server", publicServer).
		Add("health server", healthServer).
		Add("metrics server", metricsServer).
		Add("pprof server", pprofServer).
		Add("grpc health", grpcHealthJob)

	return runner, shutdown, nil
}
