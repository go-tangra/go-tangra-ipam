import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { Subnet, Vlan } from '@/api/types'

export interface VlanFilter {
  location_id?: string | undefined
  domain?: string | undefined
  status?: string | undefined
  vlan_id_min?: number | undefined
  vlan_id_max?: number | undefined
  cursor?: string | undefined
  limit?: number | undefined
}

export const useVlans = defineStore('ipam-vlans', () => {
  const items = ref<Vlan[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(filter: VlanFilter = {}): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: Vlan[] }>('GET', 'vlans', undefined, { query: { ...filter } })
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function get(id: string): Promise<Vlan> {
    return api<Vlan>('GET', 'vlans/' + id)
  }

  async function create(body: Partial<Vlan>): Promise<Vlan> {
    const v = await api<Vlan>('POST', 'vlans', body)
    items.value = [v, ...items.value]
    return v
  }

  async function update(id: string, body: Partial<Vlan>): Promise<Vlan> {
    const v = await api<Vlan>('PUT', 'vlans/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? v : x))
    return v
  }

  async function remove(id: string): Promise<void> {
    await api('DELETE', 'vlans/' + id)
    items.value = items.value.filter((x) => x.id !== id)
  }

  async function subnets(id: string): Promise<Subnet[]> {
    const res = await api<{ items: Subnet[] }>('GET', 'vlans/' + id + '/subnets')
    return res.items ?? []
  }

  return { items, loading, error, list, get, create, update, remove, subnets }
})
