package metrics

import (
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

var ErrInvalidPool = errors.New("invalid pool collector")

type PoolStats struct {
	Acquired      int32
	Idle          int32
	Total         int32
	Max           int32
	EmptyAcquires int64
}

type poolCollector struct {
	read        func() PoolStats
	connections *prometheus.Desc
	max         *prometheus.Desc
	empty       *prometheus.Desc
}

func RegisterPool(name string, read func() PoolStats) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidPool)
	case read == nil:
		return fmt.Errorf("%w: nothing to read", ErrInvalidPool)
	}

	labels := prometheus.Labels{"pool": name}

	collector := poolCollector{
		read: read,
		connections: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "db_pool_connections"),
			"Connections in the pool, by state.",
			[]string{"state"}, labels),
		max: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "db_pool_connections_max"),
			"Connections the pool is allowed to open.",
			nil, labels),
		empty: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "db_pool_empty_acquires_total"),
			"Acquires that had to wait because the pool was exhausted.",
			nil, labels),
	}

	var already prometheus.AlreadyRegisteredError

	if err := prometheus.Register(collector); err != nil && !errors.As(err, &already) {
		return fmt.Errorf("register pool %q: %w", name, err)
	}

	return nil
}

func (c poolCollector) Describe(descs chan<- *prometheus.Desc) {
	descs <- c.connections
	descs <- c.max
	descs <- c.empty
}

func (c poolCollector) Collect(out chan<- prometheus.Metric) {
	stats := c.read()

	for state, value := range map[string]int32{
		"acquired": stats.Acquired,
		"idle":     stats.Idle,
		"total":    stats.Total,
	} {
		out <- prometheus.MustNewConstMetric(
			c.connections, prometheus.GaugeValue, float64(value), state)
	}

	out <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(stats.Max))
	out <- prometheus.MustNewConstMetric(
		c.empty, prometheus.CounterValue, float64(stats.EmptyAcquires))
}
