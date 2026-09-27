package repodb

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// ---- device hardware (feature 023)

const hardwareCols = `device_id::text, tenant_id::text, profile, digest, cpu_model, cpu_sockets, cpu_cores, cpu_threads,
	memory_total_bytes, memory_type, memory_slots_total, memory_slots_used, disk_count, disk_total_bytes,
	reported_at, updated_at`

func scanHardware(sc scanner) (store.DeviceHardware, error) {
	var h store.DeviceHardware
	var profile []byte
	s := &h.Summary
	if err := sc.Scan(&h.DeviceID, &h.TenantID, &profile, &h.Digest, &s.CPUModel, &s.CPUSockets, &s.CPUCores, &s.CPUThreads,
		&s.MemoryTotalBytes, &s.MemoryType, &s.MemorySlotsTotal, &s.MemorySlotsUsed, &s.DiskCount, &s.DiskTotalBytes,
		&h.ReportedAt, &h.UpdatedAt); err != nil {
		return store.DeviceHardware{}, err
	}
	s.ReportedAt = h.ReportedAt
	if err := json.Unmarshal(profile, &h.Profile); err != nil {
		return store.DeviceHardware{}, err
	}
	return h, nil
}

// hardwareSummary reads the lifted summary of a device's hardware (nil
// when the device has none).
func hardwareSummary(ctx context.Context, tx pgx.Tx, tenantID, deviceID string) (*store.HardwareSummary, error) {
	var s store.HardwareSummary
	err := tx.QueryRow(ctx, `SELECT cpu_model, cpu_sockets, cpu_cores, cpu_threads, memory_total_bytes, memory_type,
		memory_slots_total, memory_slots_used, disk_count, disk_total_bytes, reported_at
		FROM ipam_device_hardware WHERE tenant_id=$1 AND device_id=$2`, tenantID, deviceID).Scan(
		&s.CPUModel, &s.CPUSockets, &s.CPUCores, &s.CPUThreads, &s.MemoryTotalBytes, &s.MemoryType,
		&s.MemorySlotsTotal, &s.MemorySlotsUsed, &s.DiskCount, &s.DiskTotalBytes, &s.ReportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetDeviceHardware implements repo.Store.
func (d *DB) GetDeviceHardware(ctx context.Context, tenantID, deviceID string) (out store.DeviceHardware, err error) {
	err = d.tenant(ctx, tenantID, func(tx pgx.Tx) error {
		var e error
		out, e = scanHardware(tx.QueryRow(ctx, "SELECT "+hardwareCols+" FROM ipam_device_hardware WHERE tenant_id=$1 AND device_id=$2", tenantID, deviceID))
		return mapErr(e)
	})
	return
}

// GetHardware implements repo.HostTx.
func (t *hostTx) GetHardware(deviceID string) (*store.DeviceHardware, error) {
	h, err := scanHardware(t.tx.QueryRow(t.ctx, "SELECT "+hardwareCols+" FROM ipam_device_hardware WHERE tenant_id=$1 AND device_id=$2", t.tid, deviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return &h, nil
}

// ReplaceHardware implements repo.HostTx: an upsert that sets exactly the
// profile, digest, summary and times.
func (t *hostTx) ReplaceHardware(h store.DeviceHardware) error {
	profile, err := json.Marshal(h.Profile)
	if err != nil {
		return err
	}
	s := h.Summary
	_, err = t.tx.Exec(t.ctx, `INSERT INTO ipam_device_hardware
		(device_id, tenant_id, profile, digest, cpu_model, cpu_sockets, cpu_cores, cpu_threads, memory_total_bytes,
		 memory_type, memory_slots_total, memory_slots_used, disk_count, disk_total_bytes, reported_at, updated_at)
		VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (device_id) DO UPDATE SET profile=EXCLUDED.profile, digest=EXCLUDED.digest,
		  cpu_model=EXCLUDED.cpu_model, cpu_sockets=EXCLUDED.cpu_sockets, cpu_cores=EXCLUDED.cpu_cores,
		  cpu_threads=EXCLUDED.cpu_threads, memory_total_bytes=EXCLUDED.memory_total_bytes,
		  memory_type=EXCLUDED.memory_type, memory_slots_total=EXCLUDED.memory_slots_total,
		  memory_slots_used=EXCLUDED.memory_slots_used, disk_count=EXCLUDED.disk_count,
		  disk_total_bytes=EXCLUDED.disk_total_bytes, reported_at=EXCLUDED.reported_at, updated_at=EXCLUDED.updated_at`,
		h.DeviceID, t.tid, string(profile), h.Digest, s.CPUModel, s.CPUSockets, s.CPUCores, s.CPUThreads, s.MemoryTotalBytes,
		s.MemoryType, s.MemorySlotsTotal, s.MemorySlotsUsed, s.DiskCount, s.DiskTotalBytes, h.ReportedAt, time.Now().UTC())
	return mapErr(err)
}
