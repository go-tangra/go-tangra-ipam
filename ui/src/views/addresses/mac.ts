import type { AddressLink, HostSwitchLink, IPAddress } from '@/api/types'

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

// portText is the switch (name, else id) and its port; a port name that
// already reads "Port 17" is not prefixed again.
function portText(l: AddressLink): string {
  const port = l.port_name || l.port_id
  return `${l.switch_name || l.switch_id} ${/^port\b/i.test(port) ? port : 'port ' + port}`
}

// linkText is "Connected to": the switch (name, else id), its port and VLAN.
export function linkText(l: AddressLink | undefined): string {
  if (!l) return ''
  return portText(l) + (l.vlan ? ` (VLAN ${l.vlan})` : '')
}

// sortedLinks orders per-switch links primary first (then by switch).
export function sortedLinks(links: HostSwitchLink[] | undefined): HostSwitchLink[] {
  return [...(links ?? [])].sort((a, b) => Number(b.primary) - Number(a.primary) || (a.switch_name || a.switch_id).localeCompare(b.switch_name || b.switch_id))
}

// linksText is "Connected to" for a host learned on several switches (MLAG /
// LACP bond): every switch port, primary first, joined by " + "; a VLAN
// shared by all links is shown once ("cs1 Port 17 + cs2 Port 17 (VLAN 30)").
export function linksText(links: HostSwitchLink[] | undefined): string {
  const ls = sortedLinks(links)
  const first = ls[0]
  if (!first) return ''
  if (ls.length === 1) return linkText(first)
  const vlans = new Set(ls.map((l) => l.vlan ?? 0))
  if (vlans.size === 1) return ls.map(portText).join(' + ') + (first.vlan ? ` (VLAN ${first.vlan})` : '')
  return ls.map(linkText).join(' + ')
}

// addressLinkText is an address's "Connected to": its per-switch links when
// known, else the (primary) link.
export function addressLinkText(a: IPAddress): string {
  return a.links?.length ? linksText(a.links) : linkText(a.link)
}

// linkTitle is the link's tooltip: how it was inferred and when confirmed.
export function linkTitle(l: AddressLink): string {
  const how = l.source === 'lldp' ? 'LLDP' : 'switch MAC table'
  return l.last_seen ? `${how} · last confirmed ${new Date(l.last_seen).toLocaleString()}` : how
}

// addressLinkTitle is the tooltip of an address's links: one line per switch
// (the primary marked) or the single link's tooltip.
export function addressLinkTitle(a: IPAddress): string {
  const ls = sortedLinks(a.links)
  if (ls.length > 1) return ls.map((l) => `${portText(l)}${l.primary ? ' (primary)' : ''}: ${linkTitle(l)}`).join('\n')
  const l = ls[0] ?? a.link
  return l ? linkTitle(l) : ''
}
