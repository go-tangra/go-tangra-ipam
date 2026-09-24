// Package events publishes IPAM realtime events to the shared platform event
// bus so the gateway SSE hub relays them to the browser. The module only
// publishes (it consumes no external events); payloads carry no SNMP/IPMI
// credentials, sealed owner/contact fields or secret references.
package events

import (
	"context"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/stream"
)

// Event types published to platform:events:<tenant>.
const (
	IPAddressCreated = "ipam.ip_address.created"
	IPAddressUpdated = "ipam.ip_address.updated"
	IPAddressDeleted = "ipam.ip_address.deleted"
	IPAddressScanned = "ipam.ip_address.scanned"

	ScanStarted   = "ipam.scan.started"
	ScanCompleted = "ipam.scan.completed"
)

// Publisher emits a realtime event to all of a tenant's subscribers.
type Publisher interface {
	Publish(ctx context.Context, tenantID, eventType string, payload any)
}

// HubPublisher publishes through the stream hub (nil hub is a no-op).
type HubPublisher struct{ Hub *stream.Hub }

// Publish broadcasts eventType to every subscriber of tenantID. A nil hub (or
// nil-hub publisher) is a safe no-op so callers need not branch on it.
func (p HubPublisher) Publish(ctx context.Context, tenantID, eventType string, payload any) {
	if p.Hub == nil {
		return
	}
	_, _ = p.Hub.PublishID(ctx, tenantID, nil, true, eventType, payload, true)
}

// IPAddressPayload is the content-safe payload for an address lifecycle event.
// The scan path uses IPAddressScanned as its action so consumers can tell a
// discovered address apart from an operator-created one.
func IPAddressPayload(action, id, address, subnetID, hostname, deviceID string) map[string]any {
	return map[string]any{
		"action":    action,
		"id":        id,
		"address":   address,
		"subnet_id": subnetID,
		"hostname":  hostname,
		"device_id": deviceID,
	}
}

// ScanPayload reports scan-job progress (no host contents, no credentials).
func ScanPayload(jobID, subnetID string, aliveCount, newCount int64) map[string]any {
	return map[string]any{
		"job_id":      jobID,
		"subnet_id":   subnetID,
		"alive_count": aliveCount,
		"new_count":   newCount,
	}
}
