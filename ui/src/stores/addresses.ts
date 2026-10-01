import { defineStore } from 'pinia'
import { api } from '@/api/client'
import type { IPAddress, PingResult } from '@/api/types'
import { listOptions, pagedList } from './paged'

/** Sortable fields of GET /ip-addresses (server Spec store.AddressList). */
export const ADDRESS_SORTS = ['address', 'hostname', 'mac', 'status', 'address_type', 'last_seen', 'created_at'] as const
export const ADDRESS_LIST = listOptions(ADDRESS_SORTS, 'address', 'asc')

export interface AddressFilter {
  subnet_id?: string | undefined
  device_id?: string | undefined
  status?: string | undefined
  address_type?: string | undefined
  prefix?: string | undefined
  hostname?: string | undefined
  report_state?: string | undefined
  conflict?: boolean | undefined
  mac?: string | undefined
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
  // The table page (server order). Writes do not insert rows locally: the
  // page is reloaded so a new address lands where the server's order puts it.
  const { items, total, params, filter, loading, error, listed, list, reload } = pagedList<IPAddress, AddressFilter>('ip-addresses', ADDRESS_LIST)
  const refresh = async () => (listed.value ? reload() : null)

  async function get(id: string): Promise<IPAddress> {
    return api<IPAddress>('GET', 'ip-addresses/' + id)
  }

  async function create(body: Partial<IPAddress>): Promise<IPAddress> {
    const a = await api<IPAddress>('POST', 'ip-addresses', body)
    void refresh()
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
    void refresh()
  }

  // allocate claims the next-free address in a subnet.
  async function allocate(body: AllocateRequest): Promise<IPAddress> {
    const a = await api<IPAddress>('POST', 'ip-addresses/allocate', body)
    void refresh()
    return a
  }

  // bulkAllocate claims several next-free addresses at once.
  async function bulkAllocate(body: BulkAllocateRequest): Promise<IPAddress[]> {
    const res = await api<{ items: IPAddress[] }>('POST', 'ip-addresses/bulk-allocate', body)
    void refresh()
    return res.items ?? []
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

  return { items, total, params, filter, loading, error, listed, list, reload, get, create, update, remove, allocate, bulkAllocate, find, suggest, ping }
})
