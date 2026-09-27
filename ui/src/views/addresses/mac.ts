import type { IPAddress } from '@/api/types'

// MAC provenance of an address (feature 022, FR-005 / US4).

const LABELS: Record<string, string> = { agent: 'Agent', manual: 'Manual', arp: 'ARP' }

// macSourceLabel is the short badge text ("" when the MAC has no source).
export function macSourceLabel(a: IPAddress): string {
  return LABELS[a.mac_source ?? ''] ?? ''
}

// macSourceText is the tooltip: the source, for ARP the reporting device
// (its name when known) and last-seen time, plus a MAC conflict.
export function macSourceText(a: IPAddress, deviceName: (id: string) => string): string {
  const parts: string[] = []
  const label = macSourceLabel(a)
  if (label) {
    const dev = a.mac_source === 'arp' && a.mac_source_device_id ? ` (${deviceName(a.mac_source_device_id) || a.mac_source_device_id})` : ''
    parts.push(label + dev)
  }
  if (a.mac_seen_at) parts.push('last seen ' + new Date(a.mac_seen_at).toLocaleString())
  if (a.mac_conflict) parts.push('ARP reports ' + a.mac_conflict)
  return parts.join(' · ')
}
