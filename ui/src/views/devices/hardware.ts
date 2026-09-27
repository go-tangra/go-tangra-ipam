import type { HardwareSummary } from '@/api/types'

// Memory in binary units (GiB), disks in decimal units (TB) — the units the
// vendors print on the modules and drives.
export function formatMemory(bytes?: number): string {
  if (!bytes) return ''
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${trim(v)} ${units[i]}`
}

export function formatDiskSize(bytes?: number): string {
  if (!bytes) return ''
  const units = ['B', 'kB', 'MB', 'GB', 'TB', 'PB']
  let v = bytes
  let i = 0
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000
    i++
  }
  return `${trim(v)} ${units[i]}`
}

function trim(v: number): string {
  return String(Number(v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)))
}

// hardwareSummaryLine: "2× Intel Xeon Silver 4310 · 24 cores / 48 threads ·
// 512 GiB DDR4 (16/16 slots) · 3 disks, 11.8 TB" (parts without data omitted).
export function hardwareSummaryLine(s: HardwareSummary): string {
  const parts: string[] = []
  if (s.cpu_model) parts.push((s.cpu_sockets ?? 0) > 1 ? `${s.cpu_sockets}× ${s.cpu_model}` : s.cpu_model)
  if (s.cpu_cores) parts.push(`${s.cpu_cores} cores / ${s.cpu_threads ?? 0} threads`)
  if (s.memory_total_bytes) {
    let mem = formatMemory(s.memory_total_bytes)
    if (s.memory_type) mem += ` ${s.memory_type}`
    if (s.memory_slots_total) mem += ` (${s.memory_slots_used ?? 0}/${s.memory_slots_total} slots)`
    parts.push(mem)
  }
  if (s.disk_count) parts.push(`${s.disk_count} ${s.disk_count === 1 ? 'disk' : 'disks'}, ${formatDiskSize(s.disk_total_bytes)}`)
  return parts.join(' · ')
}

export const mediaLabels: Record<string, string> = { ssd: 'SSD', hdd: 'HDD', nvme_ssd: 'NVMe SSD', unknown: 'unknown' }
