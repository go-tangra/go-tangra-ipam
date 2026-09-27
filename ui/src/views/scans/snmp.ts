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
      return `ran (${src}) · probed ${j.snmp_probed ?? 0} · discovered ${j.snmp_discovered_count ?? 0} · no answer ${j.snmp_no_answer ?? 0} · rejected ${j.snmp_rejected ?? 0}`
    }
    default:
      return ''
  }
}
