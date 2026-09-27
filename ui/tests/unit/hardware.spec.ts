import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import Detail from '@/views/devices/detail.vue'
import { formatDiskSize, formatMemory, hardwareSummaryLine } from '@/views/devices/hardware'
import type { HardwareSummary } from '@/api/types'

function fetchMock(handler: (url: string, init: RequestInit) => unknown) {
  const calls: { url: string; init: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    const body = handler(url, init)
    if (body === 404) return new Response(JSON.stringify({ reason: 'not_found' }), { status: 404, headers: { 'Content-Type': 'application/json' } })
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
class FakeSource { onopen = null; onerror = null; addEventListener() {} close() {} }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/ipam/devices', name: 'ipam-devices', component: { template: '<div/>' } }, { path: '/ipam/devices/:id', name: 'ipam-device', component: { template: '<div/>' } }] })
const withAbility = () => ({ plugins: [router, [abilitiesPlugin, createMongoAbility([{ action: 'read', subject: 'Device' }]), { useGlobalProperties: true }]] as never })

const summary: HardwareSummary = {
  cpu_model: 'Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz', cpu_sockets: 2, cpu_cores: 24, cpu_threads: 48,
  memory_total_bytes: 512 * 2 ** 30, memory_type: 'DDR4', memory_slots_total: 16, memory_slots_used: 16,
  disk_count: 3, disk_total_bytes: 11.8e12, reported_at: '2026-09-27T10:00:00Z',
}
const hardware = {
  device_id: 'd1', reported_at: '2026-09-27T10:00:00Z', summary,
  bios: { vendor: '<img src=x onerror=alert(1)>AMI', version: '2.5', release_date: '11/26/2025' },
  system: { manufacturer: 'Supermicro', product: 'Super Server', serial: 'SYS-1' },
  board: { manufacturer: 'Supermicro', product: 'X12DPi-NT6' },
  chassis: { type: 'Rack Mount Chassis' },
  processors: [{ socket: 'CPU1', model: 'Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz', cores: 12, threads: 24, populated: true }],
  memory: { total_bytes: 32 * 2 ** 30, error_correction: 'Single-bit ECC', slots_total: 2, slots_used: 1, slots: [
    { locator: 'P1-DIMMA1', populated: true, size_bytes: 32 * 2 ** 30, type: 'DDR4', form_factor: 'DIMM', type_detail: ['Registered (Buffered)'], speed_mts: 3200, configured_mts: 2666 },
    { locator: 'P1-DIMMB1' },
  ] },
  disks: [{ name: 'nvme0n1', model: 'PM9A3', serial: 'S64', size_bytes: 3.84e12, media: 'nvme_ssd', interface: 'nvme' }, { name: 'sdb', size_bytes: 16e9, media: 'unknown', interface: 'usb', removable: true }],
  filesystems: [{ mount: '/', fs: 'ext4', size_bytes: 100e9, free_bytes: 40e9, disks: ['nvme0n1'] }],
  availability: { smbios: 'ok', disks: 'ok' },
}

describe('hardware formatting', () => {
  it('summary line: CPU, cores/threads, memory with type and slots, disks', () => {
    expect(hardwareSummaryLine(summary)).toBe('2× Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz · 24 cores / 48 threads · 512 GiB DDR4 (16/16 slots) · 3 disks, 11.8 TB')
    expect(hardwareSummaryLine({ ...summary, cpu_model: '', cpu_sockets: 0, cpu_cores: 0, cpu_threads: 0, memory_type: '', memory_slots_total: 0, disk_count: 1, disk_total_bytes: 500e9 })).toBe('512 GiB · 1 disk, 500 GB')
    expect(hardwareSummaryLine({ memory_total_bytes: 0, disk_count: 0 } as HardwareSummary)).toBe('')
  })
  it('sizes', () => {
    expect(formatMemory(16 * 2 ** 30)).toBe('16 GiB')
    expect(formatMemory(1.5 * 2 ** 40)).toBe('1.5 TiB')
    expect(formatMemory(512 * 2 ** 20)).toBe('512 MiB')
    expect(formatDiskSize(3.84e12)).toBe('3.84 TB')
    expect(formatDiskSize(0)).toBe('')
  })
})

describe('device hardware tab', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.cookie = '__Host-csrf=tok; Secure; Path=/'
    vi.stubGlobal('EventSource', FakeSource)
    ;(globalThis as unknown as { __vw: number }).__vw = 1280
  })

  it('shows the hardware tab and summary row for a device with hardware; strings stay text; empty slots are greyed', async () => {
    await router.push('/ipam/devices/d1')
    fetchMock((url) => (url.endsWith('/devices/d1') ? { id: 'd1', name: 'node-1', device_type: 'server', status: 'active', source: 'host_report', hardware_summary: summary } : url.endsWith('/hardware') ? hardware : url.endsWith('/host-sync') ? { issues: [] } : { items: [] }))
    const w = mount(Detail, { global: withAbility(), attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=device-hardware-summary]').text()).toContain('24 cores / 48 threads')
    const tab = w.findAll('[role=tab]').find((t) => t.text().includes('Hardware'))!
    expect(tab).toBeTruthy()
    await tab.trigger('click')
    await flushPromises()
    const panel = w.find('[data-test=hardware-panel]')
    expect(panel.text()).toContain('<img src=x onerror=alert(1)>AMI')
    expect(panel.find('img').exists()).toBe(false)
    expect(panel.text()).toContain('Single-bit ECC')
    expect(panel.text()).toContain('Rack Mount Chassis')
    expect(w.find('[data-test="slot-row-P1-DIMMB1"]').classes()).toContain('opacity-50')
    expect(w.find('[data-test="slot-row-P1-DIMMA1"]').classes()).not.toContain('opacity-50')
    expect(panel.text()).toContain('removable')
    expect(panel.find('progress').exists()).toBe(true)
    w.unmount()
  })

  it('host-reported device without hardware: empty state; manual device: no tab', async () => {
    await router.push('/ipam/devices/d2')
    const calls = fetchMock((url) => (url.endsWith('/devices/d2') ? { id: 'd2', name: 'old', device_type: 'server', status: 'active', source: 'host_report' } : url.endsWith('/host-sync') ? { issues: [] } : { items: [] }))
    const w = mount(Detail, { global: withAbility() })
    await flushPromises()
    const tab = w.findAll('[role=tab]').find((t) => t.text().includes('Hardware'))!
    await tab.trigger('click')
    await flushPromises()
    expect(w.find('[data-test=hardware-empty]').text()).toContain('4.4.0')
    expect(calls.some((c) => c.url.endsWith('/hardware'))).toBe(false)
    expect(w.find('[data-test=device-hardware-summary]').exists()).toBe(false)
    w.unmount()

    await router.push('/ipam/devices/d3')
    fetchMock((url) => (url.endsWith('/devices/d3') ? { id: 'd3', name: 'sw', device_type: 'switch', status: 'active', source: 'manual' } : { items: [] }))
    const m = mount(Detail, { global: withAbility() })
    await flushPromises()
    expect(m.findAll('[role=tab]').map((t) => t.text()).some((t) => t.includes('Hardware'))).toBe(false)
    m.unmount()
  })

  it('editing a device never sends the server-owned hardware summary', async () => {
    const { useDevices } = await import('@/stores/devices')
    const calls = fetchMock((_url, init) => (init.method === 'PUT' ? { id: 'd1', name: 'node-1', device_type: 'server', status: 'active' } : {}))
    await useDevices().update('d1', { name: 'node-1', hardware_summary: summary } as never)
    const put = calls.find((c) => c.init.method === 'PUT')!
    expect(JSON.parse(String(put.init.body))).not.toHaveProperty('hardware_summary')
  })
})
