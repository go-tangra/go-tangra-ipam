package metrics

import (
	"context"
	"os"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	commonMetrics "github.com/go-tangra/go-tangra-common/metrics"
)

const namespace = "tangra"
const subsystem = "ipam"

// Collector holds all Prometheus metrics for the IPAM module.
type Collector struct {
	log    *log.Helper
	server *commonMetrics.MetricsServer

	// Subnet metrics
	SubnetsTotal          prometheus.Gauge
	TotalAddressesCapacity prometheus.Gauge

	// IP address metrics
	IPAddressesByStatus *prometheus.GaugeVec
	IPAddressesTotal    prometheus.Gauge

	// VLAN metrics
	VlansTotal prometheus.Gauge

	// Device metrics
	DevicesTotal prometheus.Gauge

	// Location metrics
	LocationsTotal prometheus.Gauge

	// gRPC request metrics
	RequestDuration *prometheus.HistogramVec
	RequestsTotal   *prometheus.CounterVec
}

// NewCollector creates and registers all IPAM Prometheus metrics.
func NewCollector(ctx *bootstrap.Context) *Collector {
	c := &Collector{
		log: ctx.NewLoggerHelper("ipam/metrics"),

		SubnetsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "subnets_total",
			Help:      "Total number of subnets.",
		}),

		TotalAddressesCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "total_addresses_capacity",
			Help:      "Sum of total_addresses from all subnets.",
		}),

		IPAddressesByStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "ip_addresses_by_status",
			Help:      "Number of IP addresses by status.",
		}, []string{"status"}),

		IPAddressesTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "ip_addresses_total",
			Help:      "Total number of IP address records.",
		}),

		VlansTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "vlans_total",
			Help:      "Total number of VLANs.",
		}),

		DevicesTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "devices_total",
			Help:      "Total number of devices.",
		}),

		LocationsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "locations_total",
			Help:      "Total number of locations.",
		}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "grpc_request_duration_seconds",
			Help:      "Histogram of gRPC request durations in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method"}),

		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "grpc_requests_total",
			Help:      "Total number of gRPC requests by method and status.",
		}, []string{"method", "status"}),
	}

	prometheus.MustRegister(
		c.SubnetsTotal,
		c.TotalAddressesCapacity,
		c.IPAddressesByStatus,
		c.IPAddressesTotal,
		c.VlansTotal,
		c.DevicesTotal,
		c.LocationsTotal,
		c.RequestDuration,
		c.RequestsTotal,
	)

	addr := os.Getenv("METRICS_ADDR")
	if addr == "" {
		addr = ":9410"
	}
	c.server = commonMetrics.NewMetricsServer(addr, nil, ctx.GetLogger())

	go func() {
		if err := c.server.Start(); err != nil {
			c.log.Errorf("Metrics server failed: %v", err)
		}
	}()

	return c
}

// Stop shuts down the metrics HTTP server.
func (c *Collector) Stop(ctx context.Context) {
	if c.server != nil {
		c.server.Stop(ctx)
	}
}

// Middleware returns a Kratos middleware that records gRPC request metrics.
func (c *Collector) Middleware() middleware.Middleware {
	return commonMetrics.NewServerMiddleware(c.RequestDuration, c.RequestsTotal)
}

// StatusIntToString maps IP address status integers to string labels.
func StatusIntToString(status int32) string {
	switch status {
	case 1:
		return "active"
	case 2:
		return "reserved"
	case 3:
		return "dhcp"
	case 4:
		return "deprecated"
	case 5:
		return "offline"
	default:
		return "unknown"
	}
}

// --- Subnet helpers ---

// SubnetCreated increments the subnet counter.
func (c *Collector) SubnetCreated() {
	c.SubnetsTotal.Inc()
}

// SubnetDeleted decrements the subnet counter.
func (c *Collector) SubnetDeleted() {
	c.SubnetsTotal.Dec()
}

// --- IP address helpers ---

// IPAddressCreated increments counters for a newly created IP address.
func (c *Collector) IPAddressCreated(status string) {
	c.IPAddressesByStatus.WithLabelValues(status).Inc()
	c.IPAddressesTotal.Inc()
}

// IPAddressDeleted decrements counters for a deleted IP address.
func (c *Collector) IPAddressDeleted(status string) {
	c.IPAddressesByStatus.WithLabelValues(status).Dec()
	c.IPAddressesTotal.Dec()
}

// IPAddressStatusChanged adjusts the status gauge when an IP address's status changes.
func (c *Collector) IPAddressStatusChanged(oldStatus, newStatus string) {
	c.IPAddressesByStatus.WithLabelValues(oldStatus).Dec()
	c.IPAddressesByStatus.WithLabelValues(newStatus).Inc()
}

// --- VLAN helpers ---

// VlanCreated increments the VLAN counter.
func (c *Collector) VlanCreated() {
	c.VlansTotal.Inc()
}

// VlanDeleted decrements the VLAN counter.
func (c *Collector) VlanDeleted() {
	c.VlansTotal.Dec()
}

// --- Device helpers ---

// DeviceCreated increments the device counter.
func (c *Collector) DeviceCreated() {
	c.DevicesTotal.Inc()
}

// DeviceDeleted decrements the device counter.
func (c *Collector) DeviceDeleted() {
	c.DevicesTotal.Dec()
}

// --- Location helpers ---

// LocationCreated increments the location counter.
func (c *Collector) LocationCreated() {
	c.LocationsTotal.Inc()
}

// LocationDeleted decrements the location counter.
func (c *Collector) LocationDeleted() {
	c.LocationsTotal.Dec()
}
