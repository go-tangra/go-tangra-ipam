import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useAddresses } from '@/stores/addresses'
import { useScans } from '@/stores/scans'
import type { IPAddress, IPScanJob } from '@/api/types'

// A single shared EventSource relays the module's live events through the
// gateway. ipam.ip_address.* patches the address list in place;
// ipam.scan.* patches the scan-jobs registry. The stream is reference-counted
// so several views share one connection.
export type Listener = (type: string, data: unknown) => void

const EVENTS = [
  'ipam.ip_address.created',
  'ipam.ip_address.updated',
  'ipam.ip_address.deleted',
  'ipam.ip_address.scanned',
  'ipam.scan.started',
  'ipam.scan.completed',
]

// Envelope mirrors the platform bus event contract
// {id,type,source,timestamp,tenant_id,data{...}}.
interface Envelope {
  id?: string
  type?: string
  data?: unknown
}

export const useLive = defineStore('ipam-live', () => {
  const connected = ref(false)
  let source: EventSource | null = null
  let refs = 0
  const listeners = new Set<Listener>()

  function handle(type: string, raw: string): void {
    let parsed: unknown = {}
    try {
      parsed = JSON.parse(raw)
    } catch {
      /* non-JSON payloads are ignored */
    }
    const env = (parsed ?? {}) as Envelope
    const data = (env.data ?? parsed) as Record<string, unknown>

    if (type.startsWith('ipam.ip_address.')) {
      const addresses = useAddresses()
      if (type === 'ipam.ip_address.deleted') {
        if (data && typeof data.id === 'string') addresses.drop(data.id)
      } else if (data && typeof data.id === 'string') {
        addresses.patch(data as unknown as IPAddress)
      }
    } else if (type.startsWith('ipam.scan.')) {
      const scans = useScans()
      const id = (data.job_id ?? data.id) as unknown
      if (typeof id === 'string') scans.patch({ ...(data as unknown as IPScanJob), id })
    }
    for (const l of listeners) l(type, data)
  }

  function open(): void {
    if (source) return
    source = new EventSource('/api/ipam/v1/stream', { withCredentials: true })
    source.onopen = () => (connected.value = true)
    source.onerror = () => (connected.value = false)
    for (const t of EVENTS) source.addEventListener(t, (e) => handle(t, (e as MessageEvent).data))
    source.addEventListener('message', (e) => handle((e as MessageEvent).type, (e as MessageEvent).data))
  }

  /** Opens the stream (first caller) and returns a release function. */
  function connect(): () => void {
    refs += 1
    open()
    return () => {
      refs -= 1
      if (refs <= 0) close()
    }
  }

  function close(): void {
    refs = 0
    source?.close()
    source = null
    connected.value = false
  }

  function on(l: Listener): () => void {
    listeners.add(l)
    return () => listeners.delete(l)
  }

  // Exposed for tests: inject a fake event.
  function _emit(type: string, raw: string): void {
    handle(type, raw)
  }

  return { connected, connect, close, on, _emit }
})
