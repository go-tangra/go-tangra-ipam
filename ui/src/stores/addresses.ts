import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { IPAddress, PingResult } from '@/api/types'

export interface AddressFilter {
  subnet_id?: string | undefined
  device_id?: string | undefined
  status?: string | undefined
  address_type?: string | undefined
  prefix?: string | undefined
  hostname?: string | undefined
  cursor?: string | undefined
  limit?: number | undefined
}

export interface AllocateRequest {
  subnet_id: string
  hostname?: string | undefined
  device_id?: string | undefined
  address_type?: string | undefined
  description?: string | undefined
}

export interface BulkAllocateRequest {
  subnet_id: string
  count: number
  hostname_prefix?: string | undefined
}

export const useAddresses = defineStore('ipam-addresses', () => {
  const items = ref<IPAddress[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(filter: AddressFilter = {}): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: IPAddress[] }>('GET', 'ip-addresses', undefined, { query: { ...filter } })
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function get(id: string): Promise<IPAddress> {
    return api<IPAddress>('GET', 'ip-addresses/' + id)
  }

  async function create(body: Partial<IPAddress>): Promise<IPAddress> {
    const a = await api<IPAddress>('POST', 'ip-addresses', body)
    items.value = [a, ...items.value]
    return a
  }

  async function update(id: string, body: Partial<IPAddress>): Promise<IPAddress> {
    const a = await api<IPAddress>('PUT', 'ip-addresses/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? a : x))
    return a
  }

  async function remove(id: string): Promise<void> {
    await api('DELETE', 'ip-addresses/' + id)
    items.value = items.value.filter((x) => x.id !== id)
  }

  // allocate claims the next-free address in a subnet.
  async function allocate(body: AllocateRequest): Promise<IPAddress> {
    const a = await api<IPAddress>('POST', 'ip-addresses/allocate', body)
    items.value = [a, ...items.value]
    return a
  }

  // bulkAllocate claims several next-free addresses at once.
  async function bulkAllocate(body: BulkAllocateRequest): Promise<IPAddress[]> {
    const res = await api<{ items: IPAddress[] }>('POST', 'ip-addresses/bulk-allocate', body)
    const created = res.items ?? []
    items.value = [...created, ...items.value]
    return created
  }

  // find looks up a single address by literal value.
  async function find(address: string): Promise<IPAddress> {
    return api<IPAddress>('GET', 'ip-addresses/find', undefined, { query: { address } })
  }

  // suggest returns ICMP+TCP verified free addresses in a subnet.
  async function suggest(subnetId: string, count = 5): Promise<string[]> {
    const res = await api<{ suggestions: string[] | null }>('GET', 'ip-addresses/suggest', undefined, { query: { subnet_id: subnetId, count } })
    return res.suggestions ?? []
  }

  // ping probes reachability of an address (scan:run).
  async function ping(id: string): Promise<PingResult> {
    return api<PingResult>('POST', 'ip-addresses/' + id + '/ping', {})
  }

  // patch upserts an address in place from a live ipam.ip_address.* event.
  function patch(a: IPAddress): void {
    const i = items.value.findIndex((x) => x.id === a.id)
    if (i >= 0) items.value[i] = a
    else items.value = [a, ...items.value]
  }

  function drop(id: string): void {
    items.value = items.value.filter((x) => x.id !== id)
  }

  return { items, loading, error, list, get, create, update, remove, allocate, bulkAllocate, find, suggest, ping, patch, drop }
})
