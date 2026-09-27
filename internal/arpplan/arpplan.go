// Package arpplan is the pure planner of ARP-based MAC linking (feature 022,
// research D2). It turns the ARP/neighbour entries a scan read from network
// devices into per-address operations under the MAC provenance rules: ARP
// fills an empty MAC and updates a MAC it learned itself, never changes a MAC
// the host-sync agent or a user set (a disagreement is recorded as a
// conflict), and creates an address for an IP inside a known subnet that has
// no record yet. Untrusted entries are filtered first (invalid, multicast,
// virtual-router, network-device, proxy-ARP, excluded source devices, IPs
// outside the tenant's subnets) and counted by reason.
//
// The package has no I/O: the scan executor loads the input, calls Build and
// hands the ops to the store, which applies them in one tenant transaction.
package arpplan

import (
	"time"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/scan/snmp"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// Observation is the ARP table one device returned in this scan.
type Observation struct {
	DeviceID string
	Entries  []snmp.ARPEntry
}

// Input is everything one tenant's plan needs.
type Input struct {
	TenantID string
	JobID    string
	Now      time.Time
	// NewID generates the ids of created addresses (store.NewID when nil).
	NewID        func() string
	Observations []Observation
	Subnets      []store.Subnet
	Addresses    []store.IPAddress
	// NetworkMACs are the interface MACs of the tenant's network devices.
	NetworkMACs map[string]bool
	// ProxyThreshold is the most IPs one MAC may answer for in a scan before
	// its entries are treated as proxy ARP (store default when < 1).
	ProxyThreshold  int
	ExcludedDevices []string
}

// Plan is the planned change set with its counters.
type Plan struct {
	Ops []store.ARPOp
	// Ignored counts dropped entries by reason (store.ARPIgnored*).
	Ignored map[string]int
	// Entries read, addresses whose MAC is set or changed (Applied), created,
	// and conflicts (agent/manual disagreements plus IPs reported with
	// different MACs by several devices).
	Entries, Applied, Created, Conflicts int
}
