package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/a-aleesshin/gophprofile/internal/broker/kafka"
	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/internal/worker"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/filegrpc"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/messaging"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/postgres"
	"github.com/a-aleesshin/gophprofile/profile/internal/config"
	"github.com/a-aleesshin/gophprofile/profile/internal/usecase"
)

const (
	consumerName = "profile.worker"

	jobRecover     = "recover-stale-uploads"
	jobFailStuck   = "fail-stuck-avatars"
	jobBacklog     = "observe-outbox-backlog"
	jobPruneOutbox = "prune-outbox"
	jobPruneInbox  = "prune-processed-messages"
)

func BuildWorker(
	ctx context.Context,
	cfg config.Worker,
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

	files, err := filegrpc.New(cfg.File)
	if err != nil {
		return fail(fmt.Errorf("open file client: %w", err))
	}

	closers = append(closers, func() { _ = files.Close() })

	now := clock.System{}

	profiles := postgres.NewProfiles(db)
	avatars := postgres.NewAvatars(db)

	applyProcessed := usecase.NewApplyFileProcessedHandler(avatars, profiles, db, now)
	applyRejected := usecase.NewApplyFileRejectedHandler(avatars, db, now)
	applyDeleted := usecase.NewApplyFileDeletedHandler(avatars, profiles, db, now)

	recoverUploads := usecase.NewRecoverStaleUploadsHandler(avatars, files, db, now)
	failStuck := usecase.NewFailStuckAvatarsHandler(avatars, db, now)

	producer, err := kafka.NewProducer(cfg.Kafka, db)
	if err != nil {
		return fail(fmt.Errorf("open kafka producer: %w", err))
	}

	closers = append(closers, producer.Close)

	relay, err := outbox.NewRelay(db, outbox.TransportKafka, producer, now, cfg.Relay)
	if err != nil {
		return fail(fmt.Errorf("build kafka relay: %w", err))
	}

	source, err := kafka.NewConsumer(cfg.Kafka)
	if err != nil {
		return fail(fmt.Errorf("open kafka consumer: %w", err))
	}

	closers = append(closers, source.Close)

	consumer, err := inbox.NewConsumer(consumerName, source, inbox.NewInbox(db),
		messaging.FactHandlers(applyProcessed, applyRejected, applyDeleted))
	if err != nil {
		return fail(fmt.Errorf("build fact consumer: %w", err))
	}

	recoverJob, err := worker.Every(jobRecover, cfg.Jobs.WatchdogInterval, logger,
		func(ctx context.Context) error {
			result, err := recoverUploads.Handle(ctx, usecase.RecoverStaleUploadsCommand{
				StaleAfter: cfg.Jobs.RecoverStaleAfter,
				Limit:      cfg.Jobs.WatchdogLimit,
			})
			if err != nil {
				return err
			}

			if result.Recovered+result.Failed+result.Skipped > 0 {
				logger.Info("recovered stale uploads",
					"recovered", result.Recovered,
					"failed", result.Failed,
					"skipped", result.Skipped)
			}

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobRecover, err))
	}

	failStuckJob, err := worker.Every(jobFailStuck, cfg.Jobs.WatchdogInterval, logger,
		func(ctx context.Context) error {
			result, err := failStuck.Handle(ctx, usecase.FailStuckAvatarsCommand{
				StaleAfter: cfg.Jobs.FailStuckAfter,
				Limit:      cfg.Jobs.WatchdogLimit,
			})
			if err != nil {
				return err
			}

			if result.Failed+result.Skipped > 0 {
				logger.Info("failed stuck avatars",
					"failed", result.Failed,
					"skipped", result.Skipped)
			}

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobFailStuck, err))
	}

	backlogJob, err := worker.Every(jobBacklog, cfg.Jobs.BacklogInterval, logger,
		relay.ObserveBacklog)
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobBacklog, err))
	}

	outboxPruner, err := outbox.NewPruner(db, now, cfg.OutboxPrune)
	if err != nil {
		return fail(fmt.Errorf("build outbox pruner: %w", err))
	}

	inboxPruner, err := inbox.NewPruner(db, now, cfg.InboxPrune)
	if err != nil {
		return fail(fmt.Errorf("build inbox pruner: %w", err))
	}

	pruneOutboxJob, err := worker.Every(jobPruneOutbox, cfg.Jobs.PruneInterval, logger,
		func(ctx context.Context) error {
			removed, err := outboxPruner.Prune(ctx)
			if err != nil {
				return err
			}

			if removed > 0 {
				logger.Info("pruned published outbox rows", "removed", removed)
			}

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobPruneOutbox, err))
	}

	pruneInboxJob, err := worker.Every(jobPruneInbox, cfg.Jobs.PruneInterval, logger,
		func(ctx context.Context) error {
			removed, err := inboxPruner.Prune(ctx)
			if err != nil {
				return err
			}

			if removed > 0 {
				logger.Info("pruned processed messages", "removed", removed)
			}

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobPruneInbox, err))
	}

	checks, err := health.New(cfg.Health,
		health.Check{Name: "postgres", Critical: true, Probe: pool.Ping},
		health.Check{Name: "kafka", Critical: false, Probe: producer.Ping},
	)
	if err != nil {
		return fail(fmt.Errorf("build health registry: %w", err))
	}

	healthServer, err := httpserver.New("health", checks.Handler(), cfg.HealthServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build health server: %w", err))
	}

	metricsServer, err := httpserver.New("metrics", metricsHandler(), cfg.MetricsServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build metrics server: %w", err))
	}

	runner := worker.New().
		Add("kafka relay", relay).
		Add("fact consumer", consumer).
		Add(jobBacklog, backlogJob).
		Add(jobRecover, recoverJob).
		Add(jobFailStuck, failStuckJob).
		Add(jobPruneOutbox, pruneOutboxJob).
		Add(jobPruneInbox, pruneInboxJob).
		Add("health server", healthServer).
		Add("metrics server", metricsServer)

	// pprof is opt in: unset means no profiling endpoint at all, because the one
	// it exposes needs no credentials and answers to anything on the network.
	if cfg.PprofServer.Disabled() {
		logger.Info("pprof is disabled", "reason", "no address configured")

		return runner, shutdown, nil
	}

	pprofServer, err := httpserver.New("pprof", pprofHandler(), cfg.PprofServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build pprof server: %w", err))
	}

	runner.Add("pprof server", pprofServer)

	return runner, shutdown, nil
}
