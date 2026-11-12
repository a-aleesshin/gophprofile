package config

import (
	"time"

	"github.com/a-aleesshin/gophprofile/internal/broker/kafka"
	"github.com/a-aleesshin/gophprofile/internal/config"
	"github.com/a-aleesshin/gophprofile/internal/health"
	"github.com/a-aleesshin/gophprofile/internal/httpserver"
	"github.com/a-aleesshin/gophprofile/internal/inbox"
	"github.com/a-aleesshin/gophprofile/internal/logging"
	"github.com/a-aleesshin/gophprofile/internal/outbox"
	"github.com/a-aleesshin/gophprofile/internal/pgtx"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/filegrpc"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/policy"
	"github.com/a-aleesshin/gophprofile/profile/internal/adapter/urls"
	"github.com/a-aleesshin/gophprofile/profile/internal/transport/rest"
)

type Common struct {
	Log      logging.Config
	Postgres pgtx.Config
	URLs     urls.Config
	Policy   policy.Config
}

type Observability struct {
	Health        health.Config
	HealthServer  httpserver.Config
	MetricsServer httpserver.Config
	PprofServer   httpserver.Config
}

// RecoverStaleAfter is deliberately smaller than FailStuckAfter: an abandoned
// upload can be finished by asking file, while a stuck one can only be given up
// on, and giving up must happen later than file's own reaper decides.
type Jobs struct {
	WatchdogInterval  time.Duration `env:"WATCHDOG_INTERVAL" envDefault:"5m"`
	WatchdogLimit     int           `env:"WATCHDOG_LIMIT" envDefault:"100"`
	RecoverStaleAfter time.Duration `env:"RECOVER_STALE_AFTER" envDefault:"10m"`
	FailStuckAfter    time.Duration `env:"FAIL_STUCK_AFTER" envDefault:"30m"`
	PruneInterval     time.Duration `env:"PRUNE_INTERVAL" envDefault:"1h"`
}

type Migrate struct {
	Log      logging.Config
	Postgres pgtx.Config
}

type Server struct {
	Common
	Observability

	File filegrpc.Config
	REST rest.Config
	HTTP httpserver.Config
}

type Worker struct {
	Common
	Observability

	File        filegrpc.Config
	Kafka       kafka.Config
	Relay       outbox.RelayConfig
	Jobs        Jobs
	OutboxPrune outbox.PrunerConfig
	InboxPrune  inbox.PrunerConfig
}

func load(sections ...func() error) error {
	for _, section := range sections {
		if err := section(); err != nil {
			return err
		}
	}

	return nil
}

func loadCommon(dst *Common) error {
	return load(
		func() error { return config.LoadSection(&dst.Log, "LOG_") },
		func() error { return config.LoadSection(&dst.Postgres, "POSTGRES_") },
		func() error { return config.LoadSection(&dst.URLs, "CONTENT_") },
		func() error { return config.LoadSection(&dst.Policy, "POLICY_") },
	)
}

func loadObservability(dst *Observability) error {
	return load(
		func() error { return config.LoadSection(&dst.Health, "HEALTH_") },
		func() error { return config.LoadSection(&dst.HealthServer, "HEALTH_") },
		func() error { return config.LoadSection(&dst.MetricsServer, "METRICS_") },
		func() error { return config.LoadSection(&dst.PprofServer, "PPROF_") },
	)
}

func LoadMigrate() (Migrate, error) {
	var cfg Migrate

	if err := load(
		func() error { return config.LoadSection(&cfg.Log, "LOG_") },
		func() error { return config.LoadSection(&cfg.Postgres, "POSTGRES_") },
	); err != nil {
		return Migrate{}, err
	}

	return cfg, nil
}

func LoadServer() (Server, error) {
	var cfg Server

	if err := loadCommon(&cfg.Common); err != nil {
		return Server{}, err
	}

	if err := loadObservability(&cfg.Observability); err != nil {
		return Server{}, err
	}

	if err := load(
		func() error { return config.LoadSection(&cfg.File, "FILE_") },
		func() error { return config.LoadSection(&cfg.REST, "REST_") },
		func() error { return config.LoadSection(&cfg.HTTP, "HTTP_") },
	); err != nil {
		return Server{}, err
	}

	return cfg, nil
}

func LoadWorker() (Worker, error) {
	var cfg Worker

	if err := loadCommon(&cfg.Common); err != nil {
		return Worker{}, err
	}

	if err := loadObservability(&cfg.Observability); err != nil {
		return Worker{}, err
	}

	if err := load(
		func() error { return config.LoadSection(&cfg.File, "FILE_") },
		func() error { return config.LoadSection(&cfg.Kafka, "KAFKA_") },
		func() error { return config.LoadSection(&cfg.Relay, "RELAY_") },
		func() error { return config.LoadSection(&cfg.Jobs, "JOBS_") },
		func() error { return config.LoadSection(&cfg.OutboxPrune, "PRUNE_") },
		func() error { return config.LoadSection(&cfg.InboxPrune, "PRUNE_") },
	); err != nil {
		return Worker{}, err
	}

	return cfg, nil
}
