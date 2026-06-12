package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/go-tangra/go-tangra-ipam/internal/biz"
	"github.com/go-tangra/go-tangra-ipam/internal/data"
	"github.com/go-tangra/go-tangra-ipam/internal/data/ent"
)

// metadataInterface mirrors one entry of the agent-reported interfaces array
// embedded in a device's metadata JSON (written by tangra-client). Only the
// fields needed to materialize a DeviceInterface row are decoded.
type metadataInterface struct {
	Name       string `json:"name"`
	MACAddress string `json:"mac_address"`
}

type deviceMetadataEnvelope struct {
	Interfaces []metadataInterface `json:"interfaces"`
}

// parseMetadataInterfaces extracts the interface list from a device metadata
// JSON blob. Malformed JSON yields no interfaces rather than an error — the
// metadata is free-form and best-effort.
func parseMetadataInterfaces(metadataJSON string) []metadataInterface {
	metadataJSON = strings.TrimSpace(metadataJSON)
	if metadataJSON == "" {
		return nil
	}
	var env deviceMetadataEnvelope
	if err := json.Unmarshal([]byte(metadataJSON), &env); err != nil {
		return nil
	}
	return env.Interfaces
}

// materializeMetadataInterfaces upserts DeviceInterface rows from a device's
// agent-reported metadata interfaces. The tangra-client agent reports NICs
// inside the device metadata JSON rather than as first-class interface records;
// this promotes them to rows (carrying the MAC) so that SNMP bridge-FDB link
// correlation can match a server's MAC to a switch port.
//
// It is idempotent: rows are matched by (device_id, name) and only the MAC is
// (re)set. Interfaces without a name or MAC are skipped. Returns the number of
// rows created or updated.
func materializeMetadataInterfaces(ctx context.Context, repo *data.DeviceInterfaceRepo, logger *log.Helper, deviceID, metadataJSON string) int {
	ifaces := parseMetadataInterfaces(metadataJSON)
	if len(ifaces) == 0 {
		return 0
	}

	applied := 0
	for _, mi := range ifaces {
		name := strings.TrimSpace(mi.Name)
		mac := biz.NormalizeMAC(mi.MACAddress)
		if name == "" || mac == "" {
			continue
		}

		existing, err := repo.GetByDeviceAndName(ctx, deviceID, name)
		if err != nil {
			logger.Warnf("materialize interface %s on device %s: lookup failed: %v", name, deviceID, err)
			continue
		}

		if existing != nil {
			if existing.MACAddress == mac {
				continue // already up to date
			}
			if _, err := repo.Update(ctx, existing.ID, map[string]any{"mac_address": mac}); err != nil {
				logger.Warnf("materialize interface %s on device %s: update failed: %v", name, deviceID, err)
				continue
			}
		} else {
			macVal := mac
			if _, err := repo.Create(ctx, deviceID, name, func(c *ent.DeviceInterfaceCreate) {
				c.SetMACAddress(macVal)
			}); err != nil {
				logger.Warnf("materialize interface %s on device %s: create failed: %v", name, deviceID, err)
				continue
			}
		}
		applied++
	}
	return applied
}
