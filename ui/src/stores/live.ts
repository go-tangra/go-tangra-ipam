import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useAddresses } from '@/stores/addresses'
import { useScans } from '@/stores/scans'

// A single shared EventSource relays the module's live events through the
// gateway. The address and scan tables are server pages, so an event does not
// patch or prepend rows: ipam.ip_address.* and ipam.scan.* reload the page
// being shown (the server decides whether and where the record appears). A
// burst of events — a scan reports progress and many addresses — coalesces
// into one reload per RELOAD_MS. The stream is reference-counted so several
// views share one connection.
export type Listener = (type: string, data: unknown) => void

/** Events within this window share one reload of their table. */
export const RELOAD_MS = 500

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
type Table = 'addresses' | 'scans'

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

    if (type.startsWith('ipam.ip_address.')) schedule('addresses')
    else if (type.startsWith('ipam.scan.')) schedule('scans')
    for (const l of listeners) l(type, data)
  }

  // schedule reloads a table's current page once the window of the first
  // pending event closes; later events in the window ride along. A table
  // that was never listed is left alone.
  const timers: Partial<Record<Table, ReturnType<typeof setTimeout>>> = {}
  function schedule(t: Table): void {
    if (timers[t]) return
    timers[t] = setTimeout(() => {
      delete timers[t]
      const store = t === 'addresses' ? useAddresses() : useScans()
      if (store.listed) void store.reload()
    }, RELOAD_MS)
  }
  function cancelReloads(): void {
    for (const t of Object.keys(timers) as Table[]) {
      clearTimeout(timers[t])
      delete timers[t]
    }
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
    cancelReloads()
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
