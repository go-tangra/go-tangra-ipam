import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { Location, LocationTreeNode } from '@/api/types'

export interface LocationFilter {
  parent_id?: string | undefined
  location_type?: string | undefined
  country?: string | undefined
  status?: string | undefined
  cursor?: string | undefined
  limit?: number | undefined
}

export const useLocations = defineStore('ipam-locations', () => {
  const items = ref<Location[]>([])
  const tree = ref<LocationTreeNode[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(filter: LocationFilter = {}): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: Location[] }>('GET', 'locations', undefined, { query: { ...filter } })
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function loadTree(): Promise<void> {
    try {
      const res = await api<{ tree: LocationTreeNode[] | null }>('GET', 'locations/tree')
      tree.value = res.tree ?? []
    } catch (e) {
      error.value = (e as Error).message
    }
  }

  async function get(id: string): Promise<Location> {
    return api<Location>('GET', 'locations/' + id)
  }

  async function create(body: Partial<Location>): Promise<Location> {
    const l = await api<Location>('POST', 'locations', body)
    items.value = [l, ...items.value]
    return l
  }

  async function update(id: string, body: Partial<Location>): Promise<Location> {
    const l = await api<Location>('PUT', 'locations/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? l : x))
    return l
  }

  async function remove(id: string, force = false): Promise<void> {
    await api('DELETE', 'locations/' + id, undefined, { query: { force } })
    items.value = items.value.filter((x) => x.id !== id)
  }

  return { items, tree, loading, error, list, loadTree, get, create, update, remove }
})
