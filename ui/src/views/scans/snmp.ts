import type { IPScanJob } from '@/api/types'

// snmpPhaseText explains a scan's SNMP phase (feature 021, SC-005): a reason
// whenever SNMP did not run, and the counters when it did. Empty while the
// scan has not reached the phase yet.
export function snmpPhaseText(j: IPScanJob, subnetLabel: (id: string) => string): string {
  switch (j.snmp_status) {
    case 'not_requested':
      return 'not requested'
    case 'no_live_hosts':
      return 'skipped: no live hosts'
    case 'no_credentials':
      return 'skipped: no credentials for this subnet or its parents'
    case 'credentials_unreadable':
      return 'skipped: credentials unreadable (re-enter them)'
    case 'ran': {
      const src = j.snmp_source_subnet_id && j.snmp_source_subnet_id !== j.subnet_id ? 'inherited from ' + subnetLabel(j.snmp_source_subnet_id) : 'own credentials'
      const probed = j.snmp_probed ?? 0, found = j.snmp_discovered_count ?? 0, silent = j.snmp_no_answer ?? 0, rejected = j.snmp_rejected ?? 0
      const other = Math.max(0, probed - found - silent - rejected)
      return `ran (${src}) · probed ${probed} · discovered ${found} · no answer ${silent} · rejected ${rejected}` + (other ? ` · other errors ${other}` : '')
    }
    default:
      return ''
  }
}

// arpPhaseText explains a scan's ARP phase (feature 022, FR-013): disabled,
// failed, or what the devices' ARP tables contributed. Empty when SNMP did
// not run (there is no ARP phase then).
export function arpPhaseText(j: IPScanJob): string {
  switch (j.arp_status) {
    case 'disabled':
      return 'disabled'
    case 'failed':
      return 'failed (see the service log)'
    case 'ran': {
      const ignored = Object.entries(j.arp_ignored ?? {}).filter(([, n]) => n > 0).sort(([a], [b]) => a.localeCompare(b))
      const total = ignored.reduce((sum, [, n]) => sum + n, 0)
      const parts = [`${j.arp_devices ?? 0} devices`, `${j.arp_entries ?? 0} entries`, `applied ${j.arp_applied ?? 0}`, `created ${j.arp_created ?? 0}`, `conflicts ${j.arp_conflicts ?? 0}`]
      if (total) parts.push(`ignored ${total} (${ignored.map(([r, n]) => `${r} ${n}`).join(', ')})`)
      if (j.arp_partial) parts.push(`${j.arp_partial} partial`)
      return parts.join(' · ')
    }
    default:
      return ''
  }
}
