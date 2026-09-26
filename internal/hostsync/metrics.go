package hostsync

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Metrics are the host-sync OTel instruments (research D19), exported on the
// non-public admin listener. Labels carry outcomes and kinds only — never
// tenants' report contents.
type Metrics struct {
	hosts    metric.Int64Counter
	changes  metric.Int64Counter
	skipped  metric.Int64Counter
	apply    metric.Float64Histogram
	degraded metric.Int64UpDownCounter
}

// NewMetrics builds the instruments on m (nil: no-op).
func NewMetrics(m metric.Meter) *Metrics {
	if m == nil {
		m = noop.NewMeterProvider().Meter("ipam.hostsync")
	}
	out := &Metrics{}
	out.hosts, _ = m.Int64Counter("hostsync_hosts_total", metric.WithDescription("Host reports processed, by outcome"))
	out.changes, _ = m.Int64Counter("hostsync_changes_total", metric.WithDescription("Changes written by the host sync, by kind"))
	out.skipped, _ = m.Int64Counter("hostsync_entries_skipped_total", metric.WithDescription("Report entries skipped, by reason"))
	out.apply, _ = m.Float64Histogram("hostsync_apply_seconds", metric.WithDescription("Duration of one host apply"), metric.WithUnit("s"))
	out.degraded, _ = m.Int64UpDownCounter("hostsync_degraded", metric.WithDescription("Tenants whose host sync is degraded"))
	return out
}

func (m *Metrics) host(ctx context.Context, outcome string) {
	m.hosts.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (m *Metrics) change(ctx context.Context, kind string, n int) {
	m.changes.Add(ctx, int64(n), metric.WithAttributes(attribute.String("kind", kind)))
}

func (m *Metrics) skip(ctx context.Context, reason string, n int) {
	m.skipped.Add(ctx, int64(n), metric.WithAttributes(attribute.String("reason", reason)))
}

func (m *Metrics) applied(ctx context.Context, seconds float64) { m.apply.Record(ctx, seconds) }

func (m *Metrics) degrade(ctx context.Context, delta int64) { m.degraded.Add(ctx, delta) }
