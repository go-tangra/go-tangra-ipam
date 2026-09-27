import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import { createRouter, createMemoryHistory } from 'vue-router'
import Addresses from '@/views/addresses/index.vue'
import Scans from '@/views/scans/index.vue'
import Detail from '@/views/devices/detail.vue'
import ArpSettingsCard from '@/components/ArpSettingsCard.vue'
import { arpSettingsSchema } from '@/schemas'
import { arpPhaseText } from '@/views/scans/snmp'
import { linkText, macSourceLabel, macSourceText } from '@/views/addresses/mac'
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

describe('switch-port links (US2)', () => {
  const link = { switch_id: 'sw', switch_name: 'MSW-RACK2', port_id: 'p14', port_name: '14', vlan: 30, source: 'snmp_fdb' as const, last_seen: '2026-09-27T10:00:00Z' }

  it('describes a link', () => {
    expect(linkText(link)).toBe('MSW-RACK2 port 14 (VLAN 30)')
    expect(linkText({ ...link, switch_name: '', vlan: 0 })).toBe('sw port 14')
    expect(linkText(undefined)).toBe('')
  })

  it('the address list shows where each address is connected', async () => {
    fetchMock((url) => (url.includes('/ip-addresses') ? { items: [{ ...arpAddr, link }, agentAddr] } : { items: [] }))
    const w = mount(Addresses, { global, attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=address-link-a1]').text()).toBe('MSW-RACK2 port 14 (VLAN 30)')
    expect(w.find('[data-test=address-link-a2]').exists()).toBe(false)
    w.unmount()
  })

  it('a switch port lists the addresses behind it; bound addresses show their link', async () => {
    fetchMock((url) => {
      if (url.includes('/interfaces')) return { items: [{ id: 'p14', device_id: 'sw', name: '14', behind_addresses: [{ address_id: 'a1', address: '10.1.0.5', hostname: 'printer' }, { address_id: 'a3', address: '10.1.0.9' }] }] }
      if (url.includes('/addresses')) return { items: [{ ...arpAddr, link }] }
      if (url.includes('/packages')) return { items: [] }
      return { id: 'sw', name: 'MSW-RACK2', device_type: 'switch', status: 'active' }
    })
    const w = mount(Detail, { global, attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('10.1.0.5 (printer), 10.1.0.9')
    const tab = w.findAll('[role=tab]').find((t) => t.text().includes('Addresses'))!
    await tab.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('MSW-RACK2 port 14 (VLAN 30)')
    w.unmount()
  })
})

describe('ARP settings (US3)', () => {
  const routers = [
    { id: '0190f7c2-aaaa-7c1a-9b2e-00000000ab01', name: 'fortigate', device_type: 'firewall', status: 'active' },
    { id: '0190f7c2-aaaa-7c1a-9b2e-00000000ab02', name: 'mikrotik', device_type: 'router', status: 'active' },
    { id: '0190f7c2-aaaa-7c1a-9b2e-00000000ab03', name: 'web-01', device_type: 'server', status: 'active' },
  ]
  const saved = { enabled: true, excluded_devices: [], proxy_threshold: 8, updated_by: 'u1', updated_at: '2026-09-27T10:00:00Z' }
  const handler = (calls: { body?: unknown }[]) => (url: string, init: RequestInit) => {
    if (url.includes('/arp/settings')) {
      if (init.method === 'PUT') {
        const body = JSON.parse(String(init.body))
        calls.push({ body })
        return { ...saved, ...body }
      }
      return saved
    }
    if (url.includes('/devices')) return { items: routers }
    return { items: [] }
  }

  it('validates the settings', () => {
    expect(arpSettingsSchema.safeParse({ enabled: true, proxy_threshold: '8', excluded_devices: [] }).data).toEqual({ enabled: true, proxy_threshold: 8, excluded_devices: [] })
    expect(arpSettingsSchema.safeParse({ enabled: true, proxy_threshold: 1, excluded_devices: [] }).success).toBe(false)
    expect(arpSettingsSchema.safeParse({ enabled: true, proxy_threshold: 257, excluded_devices: [] }).success).toBe(false)
    expect(arpSettingsSchema.safeParse({ enabled: true, proxy_threshold: 8, excluded_devices: ['x'] }).success).toBe(false)
  })

  it('is read-only without the configure ArpSettings ability', async () => {
    fetchMock(handler([]))
    const w = mount(ArpSettingsCard, { global, attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=arp-save]').exists()).toBe(false)
    expect(w.find('[data-test=arp-readonly]').exists()).toBe(true)
    expect(w.text()).toContain('fortigate')
    expect(w.text()).not.toContain('web-01') // only network devices are ARP sources
    w.unmount()
  })

  it('an administrator disables ARP, excludes a device and sets the threshold', async () => {
    const calls: { body?: unknown }[] = []
    fetchMock(handler(calls))
    const admin = { plugins: [router, [abilitiesPlugin, createMongoAbility([{ action: 'configure', subject: 'ArpSettings' }]), { useGlobalProperties: true }]] as never }
    const w = mount(ArpSettingsCard, { global: admin, attachTo: document.body })
    await flushPromises()
    await w.find('[data-test=arp-form] input[data-field=enabled]').setValue(false)
    await w.find('[data-test=arp-exclude-0190f7c2-aaaa-7c1a-9b2e-00000000ab01] input').setValue(true)
    const th = w.find('[data-test=arp-form] input[data-field=proxy_threshold]')
    await th.setValue('1')
    await w.find('[data-test=arp-save]').trigger('click')
    await flushPromises()
    expect(calls).toHaveLength(0)
    await th.setValue('12')
    await w.find('[data-test=arp-save]').trigger('click')
    await flushPromises()
    expect(calls[0]?.body).toEqual({ enabled: false, proxy_threshold: 12, excluded_devices: ['0190f7c2-aaaa-7c1a-9b2e-00000000ab01'] })
    expect(w.find('[data-test=arp-message]').text()).toContain('saved')
    w.unmount()
  })
})
