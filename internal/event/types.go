package event

import "time"

// EventSource identifies the publisher of IPAM events.
const EventSource = "ipam-service"

// TopicPrefix is the Redis channel namespace for IPAM events.
// Full channels look like "ipam.ip_address.created".
const TopicPrefix = "ipam"

// Event topics (suffixes appended to TopicPrefix).
const (
	TopicIPAddressCreated = "ip_address.created"
	TopicIPAddressDeleted = "ip_address.deleted"
	// TopicIPAddressUpdated fires when an IP's hostname changes (DNS moves the record).
	TopicIPAddressUpdated = "ip_address.updated"
	// TopicIPAddressScanned fires for hosts discovered by a network scan when the
	// scan's "update DNS" option is enabled. Separate from created so DNS can tell
	// scan-driven syncs apart from explicit IP creation.
	TopicIPAddressScanned = "ip_address.scanned"
)

// Envelope is the common wrapper for all IPAM events published to Redis.
// It mirrors the structure used by go-tangra-lcm so existing subscribers
// (deployer-style) can parse it uniformly.
type Envelope struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Source    string      `json:"source"`
	Timestamp time.Time   `json:"timestamp"`
	TenantID  uint32      `json:"tenant_id"`
	Data      interface{} `json:"data"`
}

// IPAddressEvent is the data payload for ip_address.* topics.
type IPAddressEvent struct {
	IPAddressID string `json:"ip_address_id"`
	TenantID    uint32 `json:"tenant_id"`
	Address     string `json:"address"`
	Hostname    string `json:"hostname,omitempty"`
	// OldHostname is set on ip_address.updated when the hostname changed, so a
	// consumer can remove the record for the previous name.
	OldHostname string `json:"old_hostname,omitempty"`
	SubnetID    string `json:"subnet_id,omitempty"`
	MACAddress  string `json:"mac_address,omitempty"`
}
