import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import HostSync from '@/views/hostsync/index.vue'
import Detail from '@/views/devices/detail.vue'
import Devices from '@/views/devices/index.vue'
import Addresses from '@/views/addresses/index.vue'
import { hostSyncSettingsSchema } from '@/schemas'

function fetchMock(handler: (url: string, init: RequestInit) => unknown) {
  const calls: { url: string; init: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return new Response(JSON.stringify(handler(url, init)), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
class FakeSource { onopen = null; onerror = null; addEventListener() {} close() {} }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/ipam/devices', name: 'ipam-devices', component: { template: '<div/>' } }, { path: '/ipam/devices/:id', name: 'ipam-device', component: { template: '<div/>' } }] })
const withAbility = (rules: { action: string; subject: string }[]) => ({ plugins: [router, [abilitiesPlugin, createMongoAbility(rules), { useGlobalProperties: true }]] as never })

const settings = { enabled: true, full_interval_minutes: 60, excluded_interfaces: ['docker*', 'veth*'], updated_by: 'u1' }
const status = (state: string) => ({ enabled: state !== 'disabled', state, last_error: state === 'degraded' ? 'inventory_unavailable' : '', hosts_reported: 3, hosts_failed: 0, devices_not_reported: 1, addresses_in_conflict: 2 })

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  vi.stubGlobal('EventSource', FakeSource)
  ;(globalThis as unknown as { __vw: number }).__vw = 1280
})

describe('host sync settings schema', () => {
  it('parses one pattern per line and rejects bad patterns and intervals', () => {
    const ok = hostSyncSettingsSchema.safeParse({ enabled: true, full_interval_minutes: '30', excluded_interfaces: 'docker*\nveth*, br-*' })
    expect(ok.success && ok.data).toEqual({ enabled: true, full_interval_minutes: 30, excluded_interfaces: ['docker*', 'veth*', 'br-*'] })
    expect(hostSyncSettingsSchema.safeParse({ enabled: true, full_interval_minutes: 14, excluded_interfaces: '' }).success).toBe(false)
    expect(hostSyncSettingsSchema.safeParse({ enabled: true, full_interval_minutes: 60, excluded_interfaces: 'eth[0]' }).success).toBe(false)
    expect(hostSyncSettingsSchema.safeParse({ enabled: true, full_interval_minutes: 60, excluded_interfaces: Array.from({ length: 65 }, (_, i) => 'p' + i).join('\n') }).success).toBe(false)
  })
})

describe('host sync page', () => {
  for (const state of ['ok', 'degraded', 'disabled']) {
    it(`renders the ${state} status`, async () => {
      fetchMock((url) => (url.endsWith('/status') ? status(state) : settings))
      const w = mount(HostSync, { global: withAbility([]) })
      await flushPromises()
      expect(w.find('[data-test=hostsync-state]').text()).toContain(state)
      expect(w.find('[data-test=hostsync-status]').text()).toContain('Addresses in conflict')
      if (state === 'degraded') expect(w.text()).toContain('inventory_unavailable')
      w.unmount()
    })
  }

  it('settings are read-only without the manage HostSync ability', async () => {
    fetchMock((url) => (url.endsWith('/status') ? status('ok') : settings))
    const w = mount(HostSync, { global: withAbility([{ action: 'read', subject: 'Device' }]) })
    await flushPromises()
    expect(w.find('[data-test=hostsync-save]').exists()).toBe(false)
    expect(w.find('[data-test=hostsync-readonly]').exists()).toBe(true)
    expect(w.find('[data-test=hostsync-resync-all]').exists()).toBe(false)
    expect(w.find('textarea').attributes('disabled')).toBeDefined()
    w.unmount()
  })

  it('an administrator saves validated settings and re-syncs all hosts', async () => {
    const calls = fetchMock((url, init) => (init.method === 'POST' ? { scheduled: true } : url.endsWith('/status') ? status('ok') : settings))
    const w = mount(HostSync, { global: withAbility([{ action: 'manage', subject: 'HostSync' }, { action: 'resync', subject: 'HostSync' }]) })
    await flushPromises()
    const ta = w.find('textarea')
    await ta.setValue('docker*\nbad pattern!')
    await w.find('[data-test=hostsync-save]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'PUT')).toBe(false)
    await ta.setValue('docker*\ntap*')
    await w.find('[data-test=hostsync-save]').trigger('click')
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')!
    expect(JSON.parse(String(put.init.body))).toEqual({ enabled: true, full_interval_minutes: 60, excluded_interfaces: ['docker*', 'tap*'] })
    await w.find('[data-test=hostsync-resync-all]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.init.method === 'POST')!.url).toBe('/api/ipam/v1/host-sync/resync')
    w.unmount()
  })
})

const reported = {
  id: 'd1', name: 'hv-01', device_type: 'server', status: 'active', source: 'host_report', inventory_host_id: 'inv-1',
  report_state: 'reported', last_report_at: '2026-09-26T10:00:00Z', update_status: 'updates_available', package_update_count: 12, security_update_count: 3, reboot_required: true,
  guest_count: 2, hypervisor_device_id: '', virtualization_kind: '',
}
function deviceHandler(overrides: Record<string, unknown> = {}) {
  return (full: string, init: RequestInit) => {
    // The device's tables are server pages (?page=…&sort=…): match the path.
    const [url = '', query = ''] = full.split('?')
    if (init.method === 'POST' && url.endsWith('/host-sync')) return { applied: true, changes: 2, issues: [] }
    if (url.endsWith('/devices/d1')) return { ...reported, ...overrides }
    if (url.endsWith('/host-sync')) return { source: 'host_report', inventory_host_id: 'inv-1', report_state: 'reported', applied_at: '2026-09-26T10:01:00Z', trigger: 'poll', changes: 3, issues: [{ field: '<img src=x onerror=alert(1)>', reason: 'invalid', count: 1 }] }
    if (url.endsWith('/guests')) return { items: [{ guest_ref: '101', name: 'vm-a', kind: 'vm', macs: ['bc:24:11:00:00:01'], guest_device_id: 'g1', guest_device_name: 'vm-a' }, { guest_ref: '102', name: 'ct-b', kind: 'container', macs: ['bc:24:11:00:00:02'] }] }
    if (url.endsWith('/interfaces')) return { items: [{ id: 'i1', device_id: 'd1', name: 'eth0', mac_address: 'aa:bb:cc:00:00:01', interface_type: 'ethernet', report_state: 'reported', remote_device_name: 'sw1', remote_interface_id: 'p1', remote_port_name: 'Gi0/2', link_vlan: 30, link_source: 'snmp_fdb' }, { id: 'i2', device_id: 'd1', name: 'bmc', interface_type: 'management', report_state: 'not_reported' }] }
    if (url.endsWith('/packages')) {
      const all = [{ name: 'openssl', current_version: '1', available_version: '2', needs_update: true, is_security_update: true }, { name: 'vim', current_version: '1', available_version: '2', needs_update: true }]
      const items = new URLSearchParams(query).get('security_only') === 'true' ? all.filter((p) => p.is_security_update) : all
      return { items, total: items.length, page: 1, page_size: 25, sort: 'name', order: 'asc' }
    }
    if (url.endsWith('/addresses')) return { items: [{ id: 'a1', address: '10.0.0.5', status: 'active', address_type: 'host', report_state: 'not_reported', conflict: true, previous_device_id: 'd9' }] }
    return { items: [] }
  }
}

describe('device view (host sync)', () => {
  it('shows source, inventory host, last report and issues as text; re-sync only with the ability', async () => {
    await router.push('/ipam/devices/d1')
    fetchMock(deviceHandler())
    const plain = mount(Detail, { global: withAbility([{ action: 'read', subject: 'Device' }]) })
    await flushPromises()
    expect(plain.text()).toContain('host report (inventory agent)')
    expect(plain.text()).toContain('inv-1')
    expect(plain.find('[data-test=device-update-status]').text()).toContain('updates available (12 packages, 3 security)')
    expect(plain.find('[data-test=device-hostsync]').text()).toContain('scheduled sync')
    expect(plain.text()).toContain('reboot required')
    expect(plain.find('[data-test=device-resync]').exists()).toBe(false)
    const issues = plain.find('[data-test=device-issues]')
    expect(issues.text()).toContain('<img src=x onerror=alert(1)>')
    expect(issues.find('img').exists()).toBe(false)
    plain.unmount()
    await router.push('/ipam/devices/d1') // the router resets when its last app unmounts
    const calls = fetchMock(deviceHandler())
    const admin = mount(Detail, { global: withAbility([{ action: 'resync', subject: 'HostSync' }]) })
    await flushPromises()
    await admin.find('[data-test=device-resync]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'POST' && c.url.endsWith('/devices/d1/host-sync'))).toBe(true)
    expect(admin.text()).toContain('2 change(s) applied')
    admin.unmount()
  })

  it('interfaces show kind, BMC and not-reported badges and the switch port; guests tab lists matched and unmatched guests', async () => {
    await router.push('/ipam/devices/d1')
    fetchMock(deviceHandler())
    const w = mount(Detail, { global: withAbility([]) })
    await flushPromises()
    expect(w.text()).toContain('sw1 Gi0/2 (VLAN 30) · MAC table')
    expect(w.text()).toContain('BMC')
    expect(w.text()).toContain('not reported')
    const guests = w.findAll('[role=tab]').find((t) => t.text().includes('Guests'))!
    await guests.trigger('click')
    await flushPromises()
    const g = w.find('[data-test=device-guests]')
    expect(g.find('a').text()).toBe('vm-a')
    expect(g.text()).toContain('ct-b')
    expect(g.text()).toContain('not in IPAM')
    expect(g.text()).toContain('bc:24:11:00:00:02')
    w.unmount()
  })

  it('packages: security-only filter; addresses: not reported, conflict and moved badges', async () => {
    await router.push('/ipam/devices/d1')
    fetchMock(deviceHandler({ source: 'manual', guest_count: 0 }))
    const w = mount(Detail, { global: withAbility([]) })
    await flushPromises()
    expect(w.find('[data-test=device-hostsync]').exists()).toBe(false)
    await w.findAll('[role=tab]').find((t) => t.text().includes('Packages'))!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('vim')
    await w.find('#pkg-security-only').setValue(true)
    await flushPromises()
    expect(w.text()).not.toContain('vim')
    expect(w.text()).toContain('openssl')
    await w.findAll('[role=tab]').find((t) => t.text().includes('Addresses'))!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('conflict')
    expect(w.text()).toContain('moved')
    w.unmount()
  })

  it('description: shown in the summary when set; editing a host-reported device PUTs it with the reported fields kept', async () => {
    await router.push('/ipam/devices/d1')
    const calls = fetchMock((url, init) => (init.method === 'PUT' ? JSON.parse(String(init.body)) : deviceHandler({ description: 'rack 2, <b>spare PSU</b>' })(url, init)))
    const w = mount(Detail, { global: withAbility([]), attachTo: document.body })
    await flushPromises()
    expect(w.text()).toContain('Description')
    expect(w.text()).toContain('rack 2, <b>spare PSU</b>')
    expect(w.find('b').exists()).toBe(false)
    await w.find('[data-test=device-edit]').trigger('click')
    await flushPromises()
    const area = document.body.querySelector<HTMLTextAreaElement>('[role=dialog] textarea[data-field=description]')!
    expect(area.value).toBe('rack 2, <b>spare PSU</b>')
    area.value = 'hypervisor for the lab'
    area.dispatchEvent(new Event('input'))
    const save = Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role=dialog] button')).find((b) => b.textContent?.trim() === 'Save')!
    save.click()
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')!
    expect(put.url).toBe('/api/ipam/v1/devices/d1')
    expect(JSON.parse(String(put.init.body))).toMatchObject({ id: 'd1', name: 'hv-01', source: 'host_report', inventory_host_id: 'inv-1', description: 'hypervisor for the lab' })
    expect(w.text()).toContain('hypervisor for the lab')
    w.unmount()
  })

  it('description: hidden from the summary when empty', async () => {
    await router.push('/ipam/devices/d1')
    fetchMock(deviceHandler())
    const w = mount(Detail, { global: withAbility([]) })
    await flushPromises()
    expect(w.text()).not.toContain('Description')
    w.unmount()
  })

  it('devices list: source column and filter', async () => {
    const calls = fetchMock(() => ({ items: [reported, { id: 'd2', name: 'sw', device_type: 'switch', status: 'active', source: 'scan' }] }))
    const w = mount(Devices, { global: withAbility([]), attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test="device-row-d1"]').text()).toContain('host report')
    expect(w.find('[data-test="device-row-d2"]').text()).toContain('scan')
    expect(calls.some((c) => c.url.includes('/api/ipam/v1/devices'))).toBe(true)
    w.unmount()
  })

  it('addresses list: conflict badge and clear action only with the ability', async () => {
    const row = { id: 'a1', address: '10.0.0.5', status: 'active', address_type: 'host', conflict: true, report_state: 'reported' }
    const calls = fetchMock((url, init) => (init.method === 'POST' ? { ...row, conflict: false } : url.includes('/subnets') ? { items: [] } : { items: [row] }))
    const noAbility = mount(Addresses, { global: withAbility([]) })
    await flushPromises()
    expect(noAbility.find('[data-test="address-row-a1"]').text()).toContain('conflict')
    expect(noAbility.find('[data-test="address-clear-a1"]').exists()).toBe(false)
    noAbility.unmount()
    const w = mount(Addresses, { global: withAbility([{ action: 'clear', subject: 'AddressConflict' }]) })
    await flushPromises()
    await w.find('[data-test="address-clear-a1"]').trigger('click')
    await flushPromises()
    expect(calls.find((c) => c.init.method === 'POST')!.url).toBe('/api/ipam/v1/ip-addresses/a1/clear-conflict')
    expect(w.find('[data-test="address-row-a1"]').text()).not.toContain('conflict')
    w.unmount()
  })
})
