import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { IPScanJob } from '@/api/types'

export interface ScanFilter {
  subnet_id?: string | undefined
  status?: string | undefined
  cursor?: string | undefined
  limit?: number | undefined
}

export interface StartScanRequest {
  subnet_id: string
  enable_snmp?: boolean | undefined
  enable_dns_update?: boolean | undefined
  skip_reverse_dns?: boolean | undefined
}

export const useScans = defineStore('ipam-scans', () => {
  const items = ref<IPScanJob[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(filter: ScanFilter = {}): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: IPScanJob[] }>('GET', 'ip-scans', undefined, { query: { ...filter } })
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function get(id: string): Promise<IPScanJob> {
    return api<IPScanJob>('GET', 'ip-scans/' + id)
  }

  // start enqueues an async discovery scan for a subnet (scan:run).
  async function start(body: StartScanRequest): Promise<IPScanJob> {
    const job = await api<IPScanJob>('POST', 'ip-scans', body)
    items.value = [job, ...items.value]
    return job
  }

  async function cancel(id: string): Promise<IPScanJob> {
    const job = await api<IPScanJob>('POST', 'ip-scans/' + id + '/cancel', {})
    items.value = items.value.map((x) => (x.id === id ? job : x))
    return job
  }

  // patch upserts a job in place from a live ipam.scan.* event.
  function patch(job: IPScanJob): void {
    const i = items.value.findIndex((x) => x.id === job.id)
    if (i >= 0) items.value[i] = job
    else items.value = [job, ...items.value]
  }

  return { items, loading, error, list, get, start, cancel, patch }
})
