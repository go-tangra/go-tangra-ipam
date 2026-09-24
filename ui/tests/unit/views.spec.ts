import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import Subnets from '@/views/subnets/index.vue'
import Addresses from '@/views/addresses/index.vue'
import Detail from '@/views/devices/detail.vue'
import Scans from '@/views/scans/index.vue'
import { subnetSchema, allocateSchema, bulkAllocateSchema, checkIpSchema, vlanSchema } from '@/schemas'

function fetchMock(handler: (url: string, init: RequestInit) => unknown) {
  const calls: { url: string; init: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    const body = handler(url, init)
    return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
class FakeSource { onopen = null; onerror = null; addEventListener() {} close() {} }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/ipam/devices', name: 'ipam-devices', component: { template: '<div/>' } }, { path: '/ipam/devices/:id', name: 'ipam-device', component: { template: '<div/>' } }] })
const withAbility = (rules: { action: string; subject: string }[]) => ({ plugins: [router, [abilitiesPlugin, createMongoAbility(rules), { useGlobalProperties: true }]] as never })
const subnet = { id: 's1', name: 'Office', cidr: '10.0.0.0/24', ip_version: 4, status: 'active', total_addresses: 254, used_addresses: 200 }

describe('ipam schemas', () => {
  it('cidr accepts v4/v6 and rejects malformed prefixes (fuzz never throws)', () => {
    expect(subnetSchema.safeParse({ name: 'a', cidr: '10.0.0.0/24' }).success).toBe(true)
    expect(subnetSchema.safeParse({ name: 'a', cidr: '2001:db8::/32' }).success).toBe(true)
    for (const bad of ['999.1.1.1/8', '10.0.0.0/33', '10.0.0.0', '10.0.0/24', 'abc', '::/129', '']) expect(subnetSchema.safeParse({ name: 'a', cidr: bad }).success, bad).toBe(false)
    for (let i = 0; i < 300; i++) {
      const s = Array.from({ length: Math.floor(Math.random() * 20) }, () => '0123456789./:abcdefg'[Math.floor(Math.random() * 20)]).join('')
      expect(() => subnetSchema.safeParse({ name: 'x', cidr: s })).not.toThrow()
    }
  })
  it('allocation and bulk limits, hostnames, check ip, vlan range', () => {
    expect(allocateSchema.safeParse({ subnet_id: 's1', hostname: 'web-01.example' }).data).toEqual({ subnet_id: 's1', hostname: 'web-01.example' })
    expect(allocateSchema.safeParse({ subnet_id: 's1', hostname: 'bad host' }).success).toBe(false)
    expect(bulkAllocateSchema.safeParse({ subnet_id: 's1', count: 0 }).success).toBe(false)
    expect(bulkAllocateSchema.safeParse({ subnet_id: 's1', count: 2000 }).success).toBe(false)
    expect(bulkAllocateSchema.safeParse({ subnet_id: 's1', count: '3', hostname_prefix: 'db' }).data).toEqual({ subnet_id: 's1', count: 3, hostname_prefix: 'db' })
    expect(checkIpSchema.safeParse({ ip: '10.0.0.5' }).success).toBe(true)
    expect(checkIpSchema.safeParse({ ip: '10.0.0' }).success).toBe(false)
    expect(vlanSchema.safeParse({ vlan_id: 4095, name: 'x' }).success).toBe(false)
    expect(vlanSchema.safeParse({ vlan_id: '10', name: 'x' }).data).toMatchObject({ vlan_id: 10 })
  })
})

describe('ipam views on the kit', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.cookie = '__Host-csrf=tok; Secure; Path=/'
    vi.stubGlobal('EventSource', FakeSource)
    ;(globalThis as unknown as { __vw: number }).__vw = 1280
  })

  it('subnets: tree + table with utilization bars; tree selection opens the subnet drawer', async () => {
    const calls = fetchMock((url) => (url.endsWith('/subnets/s2') ? { ...subnet, id: 's2', cidr: '10.0.0.0/25' } : url.includes('/subnets/tree') ? { tree: [{ ...subnet, children: [{ ...subnet, id: 's2', cidr: '10.0.0.0/25', children: [] }] }] } : { items: [subnet] }))
    const w = mount(Subnets, { global: withAbility([]), attachTo: document.body })
    await flushPromises()
    expect(w.findAll('[role=treeitem]').length).toBe(2)
    expect(w.find('[data-test="subnet-row-s1"]').exists()).toBe(true)
    expect(w.find('progress').classes()).toContain('progress-warning')
    expect(w.find('[style]').exists()).toBe(false)
    await w.findAll('[role=treeitem]')[1]!.trigger('click')
    await flushPromises()
    expect(calls.at(-1)!.url).toMatch(/\/subnets\/s2$/)
    expect(document.body.querySelector('[data-test=subnet-drawer]')?.textContent).toContain('10.0.0.0/25')
    w.unmount()
  })

  it('addresses: allocate dialog validates through the schema and posts z.output', async () => {
    const calls = fetchMock((url, init) => (init.method === 'POST' ? { id: 'a9', address: '10.0.0.9', status: 'active', address_type: 'host' } : url.includes('/subnets') ? { items: [subnet] } : { items: [{ id: 'a1', address: '10.0.0.1', status: 'active', address_type: 'gateway', is_primary: true }] }))
    const w = mount(Addresses, { global: withAbility([]), attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test="address-row-a1"]').text()).toContain('primary')
    await w.findAll('button').find((b) => b.text() === 'Allocate')!.trigger('click')
    await flushPromises()
    const dialog = document.body.querySelector('[role=dialog]')!
    const host = dialog.querySelector<HTMLInputElement>('input[data-field=hostname]')!
    host.value = 'bad host'
    host.dispatchEvent(new Event('input'))
    ;(Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Allocate') as HTMLButtonElement).click()
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'POST')).toBe(false)
    expect(dialog.querySelector('[role=alert]')?.textContent).toContain('host name')
    host.value = 'web-01'
    host.dispatchEvent(new Event('input'))
    ;(Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Allocate') as HTMLButtonElement).click()
    await flushPromises()
    const post = calls.find((c) => c.init.method === 'POST')!
    expect(post.url).toBe('/api/ipam/v1/ip-addresses/allocate')
    expect(JSON.parse(String(post.init.body))).toEqual({ subnet_id: 's1', hostname: 'web-01' })
    expect(w.find('[data-test="address-row-a9"]').exists()).toBe(true)
    w.unmount()
  })

  it('device detail: power/KVM tab only with the platform-admin abilities', async () => {
    await router.push('/ipam/devices/d1')
    fetchMock((url) => (url.endsWith('/devices/d1') ? { id: 'd1', name: 'core-sw', device_type: 'switch', status: 'active', ipmi_secret_ref: 'ref' } : url.endsWith('/power') ? { on: true } : url.endsWith('/sensors') ? { items: [{ name: 'Temp', reading: '40 C', value: 40, unit: 'C', status: 'ok' }] } : { items: [] }))
    const plain = mount(Detail, { global: withAbility([{ action: 'read', subject: 'Device' }]) })
    await flushPromises()
    expect(plain.findAll('[role=tab]').map((t) => t.text())).not.toContain('Power / KVM')
    plain.unmount()
    const admin = mount(Detail, { global: withAbility([{ action: 'control', subject: 'Power' }, { action: 'access', subject: 'Kvm' }]) })
    await flushPromises()
    const oob = admin.findAll('[role=tab]').find((t) => t.text().includes('Power / KVM'))!
    await oob.trigger('click')
    await flushPromises()
    expect(admin.find('[data-test=power-off]').exists()).toBe(true)
    expect(admin.find('[data-test=kvm-start]').exists()).toBe(true)
    expect(admin.text()).toContain('Temp')
    admin.unmount()
  })

  it('scans: start dialog posts the schema output with switches', async () => {
    const calls = fetchMock((url, init) => (init.method === 'POST' ? { id: 'j1', subnet_id: 's1', status: 'pending', progress: 0 } : url.includes('/subnets') ? { items: [subnet] } : { items: [{ id: 'j0', subnet_id: 's1', status: 'scanning', progress: 40 }] }))
    const w = mount(Scans, { global: withAbility([]), attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test="scan-row-j0"]').text()).toContain('40%')
    await w.findAll('button').find((b) => b.text().includes('Start scan'))!.trigger('click')
    await flushPromises()
    const dialog = document.body.querySelector('[role=dialog]')!
    const snmp = dialog.querySelector<HTMLInputElement>('input[data-field=enable_snmp]')!
    snmp.checked = true
    snmp.dispatchEvent(new Event('change'))
    ;(Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Start') as HTMLButtonElement).click()
    await flushPromises()
    const post = calls.find((c) => c.init.method === 'POST')!
    expect(JSON.parse(String(post.init.body))).toEqual({ subnet_id: 's1', enable_snmp: true, enable_dns_update: false })
    expect(w.find('[data-test="scan-row-j1"]').exists()).toBe(true)
    w.unmount()
  })
})
