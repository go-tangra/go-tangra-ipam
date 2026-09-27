import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import { createRouter, createMemoryHistory } from 'vue-router'
import Addresses from '@/views/addresses/index.vue'
import Scans from '@/views/scans/index.vue'
import { arpPhaseText } from '@/views/scans/snmp'
import { macSourceLabel, macSourceText } from '@/views/addresses/mac'
import type { IPAddress, IPScanJob } from '@/api/types'

// Feature 022: MAC provenance on addresses and the ARP phase of scans.

function fetchMock(handler: (url: string, init: RequestInit) => unknown) {
  const calls: { url: string; init: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return new Response(JSON.stringify(handler(url, init)), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/ipam/devices/:id', name: 'ipam-device', component: { template: '<div/>' } }] })
const global = { plugins: [router, [abilitiesPlugin, createMongoAbility([]), { useGlobalProperties: true }]] as never }

const job: IPScanJob = { id: 'j1', subnet_id: 's1', status: 'completed', progress: 100, enable_snmp: true, snmp_status: 'ran' }
const arpAddr: IPAddress = { id: 'a1', address: '10.1.0.5', subnet_id: 's1', status: 'active', address_type: 'host', mac_address: '0a:5c:d2:f1:00:05', mac_source: 'arp', mac_source_device_id: 'r1', mac_seen_at: '2026-09-27T10:00:00Z', origin: 'arp' }
const agentAddr: IPAddress = { id: 'a2', address: '10.1.0.8', subnet_id: 's1', status: 'active', address_type: 'host', mac_address: '52:54:00:00:00:08', mac_source: 'agent', mac_conflict: '0a:5c:d2:f1:00:08' }

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  vi.stubGlobal('EventSource', class { onopen = null; onerror = null; addEventListener() {} close() {} })
  ;(globalThis as unknown as { __vw: number }).__vw = 1280
})

describe('arpPhaseText', () => {
  it('explains every ARP phase outcome', () => {
    expect(arpPhaseText({ ...job })).toBe('')
    expect(arpPhaseText({ ...job, arp_status: 'disabled' })).toBe('disabled')
    expect(arpPhaseText({ ...job, arp_status: 'failed' })).toContain('failed')
    const ran = arpPhaseText({ ...job, arp_status: 'ran', arp_devices: 3, arp_partial: 1, arp_entries: 120, arp_applied: 40, arp_created: 5, arp_conflicts: 2, arp_ignored: { proxy_arp: 20, network_device: 4 } })
    expect(ran).toContain('3 devices')
    expect(ran).toContain('120 entries')
    expect(ran).toContain('applied 40')
    expect(ran).toContain('created 5')
    expect(ran).toContain('conflicts 2')
    expect(ran).toContain('ignored 24 (network_device 4, proxy_arp 20)')
    expect(ran).toContain('1 partial')
    expect(arpPhaseText({ ...job, arp_status: 'ran', arp_devices: 1, arp_entries: 2 })).not.toContain('ignored')
  })
})

describe('MAC provenance', () => {
  it('labels and describes the source', () => {
    const name = (id: string) => (id === 'r1' ? 'MikroTik' : '')
    expect(macSourceLabel(arpAddr)).toBe('ARP')
    expect(macSourceLabel(agentAddr)).toBe('Agent')
    expect(macSourceLabel({ ...agentAddr, mac_source: 'manual' })).toBe('Manual')
    expect(macSourceLabel({ ...agentAddr, mac_source: '' })).toBe('')
    expect(macSourceText(arpAddr, name)).toContain('ARP (MikroTik)')
    expect(macSourceText(arpAddr, name)).toContain('last seen')
    expect(macSourceText({ ...arpAddr, mac_source_device_id: 'gone' }, name)).toContain('ARP (gone)')
    expect(macSourceText(agentAddr, name)).toContain('Agent')
    expect(macSourceText(agentAddr, name)).toContain('ARP reports 0a:5c:d2:f1:00:08')
    expect(macSourceText({ ...agentAddr, mac_source: '', mac_conflict: '' }, name)).toBe('')
  })

  it('the address list shows the source badge and a MAC conflict', async () => {
    fetchMock((url) => (url.includes('/ip-addresses') ? { items: [arpAddr, agentAddr] } : url.includes('/devices') ? { items: [{ id: 'r1', name: 'MikroTik', device_type: 'router', status: 'active' }] } : { items: [] }))
    const w = mount(Addresses, { global, attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=mac-source-a1]').text()).toBe('ARP')
    expect(w.find('[data-test=address-row-a1]').text()).toContain('from ARP')
    expect(w.find('[data-test=mac-source-a2]').text()).toBe('Agent')
    expect(w.find('[data-test=mac-conflict-a2]').exists()).toBe(true)
    expect(w.find('[data-test=mac-conflict-a1]').exists()).toBe(false)
    w.unmount()
  })

  it('the scans table shows the ARP phase', async () => {
    fetchMock((url) => (url.includes('/ip-scans') ? { items: [{ ...job, arp_status: 'disabled' }] } : { items: [] }))
    const w = mount(Scans, { global, attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=scan-row-j1] [data-test=arp-phase]').text()).toBe('disabled')
    w.unmount()
  })
})
