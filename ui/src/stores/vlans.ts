import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import { fetchAll } from '@/api/list'
import { listOptions, pagedList } from './paged'
import type { Subnet, Vlan } from '@/api/types'

/** Sortable fields of GET /vlans (server Spec store.VlanList). */
export const VLAN_SORTS = ['vlan_id', 'name', 'domain', 'status'] as const
export const VLAN_LIST = listOptions(VLAN_SORTS, 'vlan_id', 'asc')

export interface VlanFilter {
  location_id?: string | undefined
  domain?: string | undefined
  status?: string | undefined
  vlan_id_min?: number | undefined
  vlan_id_max?: number | undefined
}

export const useVlans = defineStore('ipam-vlans', () => {
  // The table page (server order) and every VLAN (pick lists, clash names).
  const { items, total, params, filter, loading, error, listed, list, reload } = pagedList<Vlan, VlanFilter>('vlans', VLAN_LIST)
  const refresh = async () => (listed.value ? reload() : null)
  const all = ref<Vlan[]>([])

  async function loadAll(): Promise<void> {
    try {
      all.value = await fetchAll<Vlan>('vlans')
    } catch (e) {
      error.value = (e as Error).message
    }
  }

  async function get(id: string): Promise<Vlan> {
    return api<Vlan>('GET', 'vlans/' + id)
  }

  async function create(body: Partial<Vlan>): Promise<Vlan> {
    const v = await api<Vlan>('POST', 'vlans', body)
    void refresh()
    return v
  }

  async function update(id: string, body: Partial<Vlan>): Promise<Vlan> {
    const v = await api<Vlan>('PUT', 'vlans/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? v : x))
    return v
  }

  // force deletes a VLAN that still has subnets bound (they keep existing,
  // without a VLAN); without it the server refuses with a conflict.
  async function remove(id: string, force = false): Promise<void> {
    await api('DELETE', 'vlans/' + id, undefined, { query: { force } })
    items.value = items.value.filter((x) => x.id !== id)
    void refresh()
  }

  async function subnets(id: string): Promise<Subnet[]> {
    const res = await api<{ items: Subnet[] }>('GET', 'vlans/' + id + '/subnets')
    return res.items ?? []
  }

  return { items, total, params, filter, all, loading, error, listed, list, reload, loadAll, get, create, update, remove, subnets }
})
