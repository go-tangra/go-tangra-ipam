package hostplan

import (
	"strings"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// How a report was matched to a device.
const (
	MatchHostID  = "host_id"
	MatchSerial  = "serial"
	MatchName    = "name"
	MatchCreated = "created"
)

// Candidates are the devices the store found for a report: the one linked to
// the inventory host, those with the reported serial, and those whose name
// equals one of CandidateNames (case-insensitive).
type Candidates struct {
	ByHost   *store.Device
	BySerial []store.Device
	ByName   []store.Device
}

// placeholderSerials never identify a machine (research D7).
var placeholderSerials = map[string]bool{
	"": true, "0": true, "none": true, "default string": true, "to be filled by o.e.m.": true,
	"system serial number": true, "not specified": true, "123456789": true, "n/a": true, "na": true,
	"chassis serial number": true, "not applicable": true, "unknown": true,
}

// genericNames are hostnames never used to adopt an existing device.
var genericNames = map[string]bool{
	"": true, "localhost": true, "localhost.localdomain": true, "ubuntu": true, "debian": true,
	"raspberrypi": true, "centos": true, "fedora": true, "unknown": true, "host": true, "server": true,
}

// IsPlaceholderSerial reports whether a serial number cannot identify a host.
func IsPlaceholderSerial(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if placeholderSerials[s] {
		return true
	}
	return strings.Trim(s, "0x") == "" // all zeros / all x
}

// IsGenericName reports whether a hostname cannot identify a host.
func IsGenericName(s string) bool { return genericNames[strings.ToLower(strings.TrimSpace(s))] }

// FallbackName is the device name of a host without a usable hostname.
func FallbackName(hostID string) string { return "host-" + short(hostID) }

func short(hostID string) string {
	if len(hostID) > 8 {
		return hostID[:8]
	}
	return hostID
}

// CollisionName is the name of a new device whose hostname is taken.
func CollisionName(hostname, hostID string) string { return hostname + " (" + short(hostID) + ")" }

// CandidateNames are the names the store must look up for a report: the
// hostname (for matching and collisions) and the collision name.
func CandidateNames(r hostreport.Report) []string {
	base := baseName(r)
	return []string{base, CollisionName(base, r.HostID)}
}

func baseName(r hostreport.Report) string {
	if r.Hostname == "" {
		return FallbackName(r.HostID)
	}
	return r.Hostname
}

func linkedElsewhere(d store.Device, hostID string) bool {
	return d.InventoryHostID != "" && !strings.EqualFold(d.InventoryHostID, hostID)
}

// Match picks the device a report updates (FR-009, research D7): the device
// linked to the inventory host; else the only device with the same
// non-placeholder serial that is not linked to another host; else the device
// named like a non-generic hostname that is not linked to another host.
// Otherwise the report creates a device.
func Match(c Candidates, r hostreport.Report) (*store.Device, string) {
	if c.ByHost != nil {
		d := *c.ByHost
		return &d, MatchHostID
	}
	if !IsPlaceholderSerial(r.Serial) {
		var hits []store.Device
		for _, d := range c.BySerial {
			if strings.EqualFold(strings.TrimSpace(d.SerialNumber), strings.TrimSpace(r.Serial)) {
				hits = append(hits, d)
			}
		}
		if len(hits) == 1 && !linkedElsewhere(hits[0], r.HostID) {
			return &hits[0], MatchSerial
		}
	}
	if r.Hostname != "" && !IsGenericName(r.Hostname) {
		for _, d := range c.ByName {
			if strings.EqualFold(d.Name, r.Hostname) && !linkedElsewhere(d, r.HostID) {
				return &d, MatchName
			}
		}
	}
	return nil, MatchCreated
}

// desiredName is the device name for a report: the hostname, or the
// collision name when another device already carries it.
func desiredName(c Candidates, r hostreport.Report, selfID string) string {
	base := baseName(r)
	taken := func(name string) bool {
		for _, d := range c.ByName {
			if d.ID != selfID && strings.EqualFold(d.Name, name) {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	return CollisionName(base, r.HostID)
}
