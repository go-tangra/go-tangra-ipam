import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { IPScanJob, SNMPTestResult, SplitResult, Subnet, SubnetSNMPInput, SubnetSNMPStatus, SubnetStats, SubnetTreeNode } from '@/api/types'

export interface SubnetFilter {
  vlan_id?: string | undefined
  parent_id?: string | undefined
  location_id?: string | undefined
  status?: string | undefined
  ip_version?: number | undefined
  query?: string | undefined
  cursor?: string | undefined
  limit?: number | undefined
}

export const useSubnets = defineStore('ipam-subnets', () => {
  const items = ref<Subnet[]>([])
  const tree = ref<SubnetTreeNode[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(filter: SubnetFilter = {}): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: Subnet[] }>('GET', 'subnets', undefined, { query: { ...filter } })
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
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
    items.value = [s, ...items.value]
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

  // testSnmp probes one address of the subnet with its effective credentials
  // (scan:run, rate-limited server side).
  async function testSnmp(id: string, address: string): Promise<SNMPTestResult> {
    return api<SNMPTestResult>('POST', 'subnets/' + id + '/snmp/test', { address })
  }

  return { items, tree, loading, error, list, loadTree, get, stats, create, update, remove, scan, split, snmpStatus, setSnmp, testSnmp }
})
