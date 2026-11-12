package app

import (
	"context"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/a-aleesshin/gophprofile/file/internal/adapter/content"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/messaging"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/postgres"
	"github.com/a-aleesshin/gophprofile/file/internal/adapter/s3"
	"github.com/a-aleesshin/gophprofile/file/internal/config"
	"github.com/a-aleesshin/gophprofile/file/internal/usecase"
	"github.com/a-aleesshin/gophprofile/internal/broker/kafka"
	"github.com/a-aleesshin/gophprofile/internal/broker/rabbitmq"
	"github.com/a-aleesshin/gophprofile/internal/clock"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/internal/worker"
)

const (
	consumerName = "file.worker"

	jobReconcile   = "reconcile-uploads"
	jobReap        = "reap-stale-files"
	jobPruneOutbox = "prune-outbox"
	jobPruneInbox  = "prune-processed-messages"
)

type Closer func()

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

	store, err := s3.New(cfg.S3, db)
	if err != nil {
		return fail(fmt.Errorf("open object storage: %w", err))
	}

	policies, err := policy.Load(cfg.Policies.Path)
	if err != nil {
		return fail(fmt.Errorf("load policies: %w", err))
	}

	processor, err := content.NewProcessor(policies)
	if err != nil {
		return fail(fmt.Errorf("build content processor: %w", err))
	}

	now := clock.System{}

	uploads := postgres.NewUploads(db)
	files := postgres.NewFiles(db)
	variants := postgres.NewVariants(db)
	publisher := messaging.NewPublisher(outbox.NewWriter(db, now))

	completeUpload := usecase.NewCompleteUploadHandler(
		policies, uploads, files, store, publisher, db, now)
	processFile := usecase.NewProcessFileHandler(
		files, variants, store, processor, publisher, db, now)
	deleteObjects := usecase.NewDeleteObjectsHandler(files, store, publisher, db, now)
	reconcile := usecase.NewReconcileUploadsHandler(uploads, uploads, store, completeUpload, now)
	reap := usecase.NewReapStaleFilesHandler(
		files, files, variants, publisher, publisher, db, now)

	connection, err := amqp.Dial(cfg.Rabbit.URL)
	if err != nil {
		return fail(fmt.Errorf("dial rabbitmq: %w", err))
	}

	closers = append(closers, func() { _ = connection.Close() })

	routingKeys := []string{messaging.DestinationProcessFile, messaging.DestinationDeleteObjects}

	if err := rabbitmq.Declare(ctx, connection, cfg.Rabbit, routingKeys); err != nil {
		return fail(fmt.Errorf("declare rabbitmq topology: %w", err))
	}

	rabbitPublisher, err := rabbitmq.NewPublisher(connection, cfg.Rabbit, db)
	if err != nil {
		return fail(fmt.Errorf("open rabbitmq publisher: %w", err))
	}

	closers = append(closers, func() { _ = rabbitPublisher.Close() })

	kafkaProducer, err := kafka.NewProducer(cfg.Kafka, db)
	if err != nil {
		return fail(fmt.Errorf("open kafka producer: %w", err))
	}

	closers = append(closers, kafkaProducer.Close)

	rabbitRelay, err := outbox.NewRelay(
		db, outbox.TransportRabbit, rabbitPublisher, now, cfg.Relay)
	if err != nil {
		return fail(fmt.Errorf("build rabbit relay: %w", err))
	}

	kafkaRelay, err := outbox.NewRelay(db, outbox.TransportKafka, kafkaProducer, now, cfg.Relay)
	if err != nil {
		return fail(fmt.Errorf("build kafka relay: %w", err))
	}

	commands := messaging.CommandHandlers(processFile, deleteObjects)
	messageInbox := inbox.NewInbox(db)

	runner := worker.New().
		Add("rabbit relay", rabbitRelay).
		Add("kafka relay", kafkaRelay)

	for _, routingKey := range routingKeys {
		source, consumerErr := rabbitmq.NewConsumer(
			connection, cfg.Rabbit, routingKey, consumerName+"."+routingKey)
		if consumerErr != nil {
			return fail(fmt.Errorf("open consumer for %s: %w", routingKey, consumerErr))
		}

		closers = append(closers, func() { _ = source.Close() })

		consumer, consumerErr := inbox.NewConsumer(consumerName, source, messageInbox, commands)
		if consumerErr != nil {
			return fail(fmt.Errorf("build consumer for %s: %w", routingKey, consumerErr))
		}

		runner.Add("consumer "+routingKey, consumer)
	}

	reconcileJob, err := worker.Every(jobReconcile, cfg.Jobs.ReconcileInterval, logger,
		func(ctx context.Context) error {
			result, err := reconcile.Handle(ctx, usecase.ReconcileUploadsCommand{
				Limit: cfg.Jobs.ReconcileLimit,
			})
			if err != nil {
				return err
			}

			if result.Completed+result.Expired+result.Failed > 0 {
				logger.Info("reconciled uploads",
					"completed", result.Completed,
					"expired", result.Expired,
					"failed", result.Failed)
			}

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobReconcile, err))
	}

	reapJob, err := worker.Every(jobReap, cfg.Jobs.ReapInterval, logger,
		func(ctx context.Context) error {
			result, err := reap.Handle(ctx, usecase.ReapStaleFilesCommand{
				Limit:      cfg.Jobs.ReapLimit,
				StaleAfter: cfg.Jobs.ReapStaleAfter,
			})
			if err != nil {
				return err
			}

			if result.Rejected+result.Requeued+result.Failed > 0 {
				logger.Info("reaped stale files",
					"rejected", result.Rejected,
					"requeued", result.Requeued,
					"failed", result.Failed)
			}

			return nil
		})
	if err != nil {
		return fail(fmt.Errorf("build %s: %w", jobReap, err))
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

	runner.
		Add(jobReconcile, reconcileJob).
		Add(jobReap, reapJob).
		Add(jobPruneOutbox, pruneOutboxJob).
		Add(jobPruneInbox, pruneInboxJob)

	checks, err := health.New(cfg.Health,
		health.Check{Name: "postgres", Critical: true, Probe: pool.Ping},
		health.Check{Name: "s3", Critical: true, Probe: store.Ping},
		health.Check{
			Name:  "rabbitmq",
			Probe: func(context.Context) error { return rabbitAlive(connection) },
		},
		health.Check{Name: "kafka", Critical: false, Probe: kafkaProducer.Ping},
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

	pprofServer, err := httpserver.New("pprof", pprofHandler(), cfg.PprofServer, logger)
	if err != nil {
		return fail(fmt.Errorf("build pprof server: %w", err))
	}

	runner.
		Add("health server", healthServer).
		Add("metrics server", metricsServer).
		Add("pprof server", pprofServer)

	return runner, shutdown, nil
}

func rabbitAlive(connection *amqp.Connection) error {
	if connection.IsClosed() {
		return fmt.Errorf("connection is closed")
	}

	return nil
}
