import { defineStore } from 'pinia'
import { api } from '@/api/client'
import { fetchPage } from '@/api/list'
import type { IPScanJob } from '@/api/types'
import { listOptions, pagedList } from './paged'

/** Sortable fields of GET /ip-scans (server Spec store.ScanList). */
export const SCAN_SORTS = ['created_at', 'status', 'subnet'] as const
export const SCAN_LIST = listOptions(SCAN_SORTS, 'created_at', 'desc')

export interface ScanFilter {
  subnet_id?: string | undefined
  status?: string | undefined
}

export interface StartScanRequest {
  subnet_id: string
  enable_snmp?: boolean | undefined
  enable_dns_update?: boolean | undefined
  skip_reverse_dns?: boolean | undefined
}

export const useScans = defineStore('ipam-scans', () => {
  // The table page (newest first by default). Live scan events reload it
  // (stores/live.ts) instead of patching rows in.
  const { items, total, params, filter, loading, error, listed, list, reload } = pagedList<IPScanJob, ScanFilter>('ip-scans', SCAN_LIST)
  const refresh = async () => (listed.value ? reload() : null)

  // count returns how many jobs match a filter (one-row page, total only).
  async function count(f: ScanFilter = {}): Promise<number> {
    return (await fetchPage<IPScanJob>('ip-scans', { ...f, page: 1, page_size: 1 })).total
  }

  async function get(id: string): Promise<IPScanJob> {
    return api<IPScanJob>('GET', 'ip-scans/' + id)
  }

  // start enqueues an async discovery scan for a subnet (scan:run).
  async function start(body: StartScanRequest): Promise<IPScanJob> {
    const job = await api<IPScanJob>('POST', 'ip-scans', body)
    void refresh()
    return job
  }

  async function cancel(id: string): Promise<IPScanJob> {
    const job = await api<IPScanJob>('POST', 'ip-scans/' + id + '/cancel', {})
    items.value = items.value.map((x) => (x.id === id ? job : x))
    return job
  }

  return { items, total, params, filter, loading, error, listed, list, reload, count, get, start, cancel }
})
