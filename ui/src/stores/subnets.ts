import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { ScanResult, Subnet, SubnetStats, SubnetTreeNode } from '@/api/types'

export interface SubnetFilter {
  vlan_id?: string | undefined
  parent_id?: string | undefined
  location_id?: string | undefined
  status?: string | undefined
  ip_version?: number | undefined
  q?: string | undefined
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
      const res = await api<{ items: SubnetTreeNode[] }>('GET', 'subnets/tree')
      tree.value = res.items ?? []
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

  // scan runs synchronous discovery on a subnet (scan:run).
  async function scan(id: string): Promise<ScanResult> {
    return api<ScanResult>('POST', 'subnets/' + id + '/scan', {})
  }

  return { items, tree, loading, error, list, loadTree, get, stats, create, update, remove, scan }
})
