import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/list'
import { listOptions, pagedList } from './paged'
import type { IPScanJob, SNMPTestResult, SplitResult, Subnet, SubnetSNMPInput, SubnetSNMPStatus, SubnetStats, SubnetTreeNode } from '@/api/types'

/** Sortable fields of GET /subnets (server Spec store.SubnetList). */
export const SUBNET_SORTS = ['cidr', 'name', 'vlan', 'location', 'status'] as const
export const SUBNET_LIST = listOptions(SUBNET_SORTS, 'cidr', 'asc')

export interface SubnetFilter {
  vlan_id?: string | undefined
  parent_id?: string | undefined
  location_id?: string | undefined
  status?: string | undefined
  ip_version?: number | undefined
  query?: string | undefined
}

export const useSubnets = defineStore('ipam-subnets', () => {
  // The table page (server order) ...
  const { items, total, params, filter, loading, error, listed, list, reload } = pagedList<Subnet, SubnetFilter>('subnets', SUBNET_LIST)
  const refresh = async () => (listed.value ? reload() : null)
  // ... and every subnet, for pick lists, parent names and a subnet's children.
  const all = ref<Subnet[]>([])
  const tree = ref<SubnetTreeNode[]>([])

  async function loadAll(): Promise<void> {
    try {
      all.value = await fetchAll<Subnet>('subnets', { sort: 'cidr' })
    } catch (e) {
      error.value = (e as Error).message
    }
  }

  async function loadTree(): Promise<void> {
    try {
      const res = await api<{ tree: SubnetTreeNode[] | null }>('GET', 'subnets/tree')
      tree.value = res.tree ?? []
    } catch (e) {
      error.value = (e as Error).message
    }
  }

  async function get(id: string): Promise<Subnet> {
    return api<Subnet>('GET', 'subnets/' + id)
  }

  async function stats(id: string): Promise<SubnetStats> {
    return api<SubnetStats>('GET', 'subnets/' + id + '/stats')
  }

  async function create(body: Partial<Subnet>): Promise<Subnet> {
    const s = await api<Subnet>('POST', 'subnets', body)
    void refresh()
    return s
  }

  async function update(id: string, body: Partial<Subnet>): Promise<Subnet> {
    const s = await api<Subnet>('PUT', 'subnets/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? s : x))
    return s
  }

  async function remove(id: string, force = false): Promise<void> {
    await api('DELETE', 'subnets/' + id, undefined, { query: { force } })
    items.value = items.value.filter((x) => x.id !== id)
    void refresh()
  }

  // scan queues a discovery job for a subnet (scan:run) and returns it at once;
  // follow it through the scans store until it reaches a terminal status.
  async function scan(id: string): Promise<IPScanJob> {
    return api<IPScanJob>('POST', 'subnets/' + id + '/scan', {})
  }

  // split carves a subnet into its /prefixLength children in one call; with
  // dryRun it only previews which blocks would be created or skipped.
  async function split(id: string, prefixLength: number, dryRun = false): Promise<SplitResult> {
    return api<SplitResult>('POST', 'subnets/' + id + '/split', { prefix_length: prefixLength, dry_run: dryRun })
  }

  // SNMP credentials (feature 021) are write-only: reads return the status,
  // never a value.
  async function snmpStatus(id: string): Promise<SubnetSNMPStatus> {
    return api<SubnetSNMPStatus>('GET', 'subnets/' + id + '/snmp')
  }

  async function setSnmp(id: string, body: SubnetSNMPInput): Promise<SubnetSNMPStatus> {
    return api<SubnetSNMPStatus>('PUT', 'subnets/' + id + '/snmp', body)
  }

  // clearSnmp deletes the subnet's own credentials; it then inherits again.
  async function clearSnmp(id: string): Promise<void> {
    await api('DELETE', 'subnets/' + id + '/snmp')
  }

  // testSnmp probes one address of the subnet with its effective credentials
  // (scan:run, rate-limited server side).
  async function testSnmp(id: string, address: string): Promise<SNMPTestResult> {
    return api<SNMPTestResult>('POST', 'subnets/' + id + '/snmp/test', { address })
  }

  return { items, total, params, filter, all, tree, loading, error, listed, list, reload, loadAll, loadTree, get, stats, create, update, remove, scan, split, snmpStatus, setSnmp, clearSnmp, testSnmp }
})
