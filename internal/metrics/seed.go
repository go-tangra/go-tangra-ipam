package metrics

import (
	"context"

	"github.com/go-tangra/go-tangra-ipam/internal/data"
)

// Seed loads initial gauge values from the database.
// Called once at startup so Prometheus has accurate values from the start.
func (c *Collector) Seed(ctx context.Context, statsRepo *data.StatisticsRepo) {
	c.log.Info("Seeding Prometheus metrics from database...")

	// Subnets
	subnetCount, err := statsRepo.GetSubnetCount(ctx)
	if err != nil {
		c.log.Errorf("Failed to seed subnet count: %v", err)
	} else {
		c.SubnetsTotal.Set(float64(subnetCount))
	}

	// Total addresses capacity (sum from subnets)
	totalAddresses, err := statsRepo.GetTotalAddresses(ctx)
	if err != nil {
		c.log.Errorf("Failed to seed total addresses capacity: %v", err)
	} else {
		c.TotalAddressesCapacity.Set(float64(totalAddresses))
	}

	// IP addresses total
	ipTotal, err := statsRepo.GetTotalAddressCount(ctx)
	if err != nil {
		c.log.Errorf("Failed to seed IP address total count: %v", err)
	} else {
		c.IPAddressesTotal.Set(float64(ipTotal))
	}

	// IP addresses by status
	statusMap := map[int32]string{
		1: "active",
		2: "reserved",
		3: "dhcp",
		4: "deprecated",
		5: "offline",
	}
	for statusInt, statusStr := range statusMap {
		count, err := statsRepo.GetAddressCountByStatus(ctx, statusInt)
		if err != nil {
			c.log.Errorf("Failed to seed IP address count for status %s: %v", statusStr, err)
		} else {
			c.IPAddressesByStatus.WithLabelValues(statusStr).Set(float64(count))
		}
	}

	// VLANs
	vlanCount, err := statsRepo.GetVlanCount(ctx)
	if err != nil {
		c.log.Errorf("Failed to seed VLAN count: %v", err)
	} else {
		c.VlansTotal.Set(float64(vlanCount))
	}

	// Devices
	deviceCount, err := statsRepo.GetDeviceCount(ctx)
	if err != nil {
		c.log.Errorf("Failed to seed device count: %v", err)
	} else {
		c.DevicesTotal.Set(float64(deviceCount))
	}

	// Locations
	locationCount, err := statsRepo.GetLocationCount(ctx)
	if err != nil {
		c.log.Errorf("Failed to seed location count: %v", err)
	} else {
		c.LocationsTotal.Set(float64(locationCount))
	}

	c.log.Info("Prometheus metrics seeded successfully")
}
