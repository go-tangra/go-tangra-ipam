import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import Subnets from '@/views/subnets/index.vue'
import Groups from '@/views/groups/index.vue'
import Locations from '@/views/locations/index.vue'
import Rack from '@/views/locations/rack.vue'
import Addresses from '@/views/addresses/index.vue'
import { ipGroupMemberSchema, locationSchema, rackPlacementSchema, splitSchema, subnetSchema } from '@/schemas'
import { mergeEdit } from '@/api/merge'
import type { Device } from '@/api/types'

type Call = { url: string; init: RequestInit }
function fetchMock(handler: (url: string, init: RequestInit) => unknown) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return new Response(JSON.stringify(handler(url, init) ?? {}), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/ipam/devices/:id', name: 'ipam-device', component: { template: '<div/>' } }] })
const global = { plugins: [router, [abilitiesPlugin, createMongoAbility([]), { useGlobalProperties: true }]] as never }
const body = (c: Call) => JSON.parse(String(c.init.body))
const dialog = () => document.body.querySelector('[role=dialog]')!
function setField(name: string, value: string): void {
  const el = dialog().querySelector<HTMLInputElement | HTMLSelectElement>(`input[data-field=${name}], select[data-field=${name}], textarea[data-field=${name}]`)!
  el.value = value
  el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input'))
}
function clickIn(root: ParentNode, text: string): void {
  const b = Array.from(root.querySelectorAll('button')).find((x) => x.textContent?.trim().startsWith(text))
  if (!b) throw new Error('no button ' + text)
  ;(b as HTMLButtonElement).click()
}
// Opens a subnet's drawer from its table row and clicks one of its actions.
async function drawerAction(w: { find: (s: string) => { trigger: (e: string) => Promise<void> } }, id: string, test: string): Promise<void> {
  await w.find(`[data-test="subnet-row-${id}"]`).trigger('click')
  await flushPromises()
  const b = document.body.querySelector<HTMLButtonElement>(`[data-test=subnet-drawer] [data-test=${test}]`)
  if (!b) throw new Error('no drawer action ' + test)
  b.click()
  await flushPromises()
}
const drawerEl = () => document.body.querySelector('[data-test=subnet-drawer]')!
const dev = (over: Partial<Device>): Device => ({ id: 'd', name: 'dev', device_type: 'server', status: 'active', ...over }) as Device

describe('ipam write schemas', () => {
  it('subnet carries parent and gateway; split prefix is bounded', () => {
    expect(subnetSchema.parse({ name: 'a', cidr: '10.0.1.0/24', parent_id: 'p1', gateway: '10.0.1.1' })).toMatchObject({ parent_id: 'p1', gateway: '10.0.1.1' })
    expect(subnetSchema.safeParse({ name: 'a', cidr: '10.0.1.0/24', gateway: 'nope' }).success).toBe(false)
    expect(splitSchema.parse({ prefix_length: '26' })).toEqual({ prefix_length: 26 })
    for (const bad of [0, 129, 'x', 25.5]) expect(splitSchema.safeParse({ prefix_length: bad }).success, String(bad)).toBe(false)
  })
  it('group member values follow their type', () => {
    const ok = (member_type: string, value: string) => ipGroupMemberSchema.safeParse({ member_type, value, sequence: 1 }).success
    expect(ok('address', '10.0.0.5')).toBe(true)
    expect(ok('address', '10.0.0.0/24')).toBe(false)
    expect(ok('subnet', '10.0.0.0/24')).toBe(true)
    expect(ok('range', '10.0.0.10-10.0.0.20')).toBe(true)
    expect(ok('range', '10.0.0.10')).toBe(false)
  })
  it('a rack needs a height; placement needs a unit ≥ 1', () => {
    expect(locationSchema.safeParse({ name: 'R1', location_type: 'rack', rack_size_u: 0 }).success).toBe(false)
    expect(locationSchema.safeParse({ name: 'R1', location_type: 'rack', rack_size_u: 42 }).success).toBe(true)
    expect(locationSchema.safeParse({ name: 'Room', location_type: 'room', rack_size_u: 0 }).success).toBe(true)
    expect(rackPlacementSchema.safeParse({ device_id: 'd1', rack_position: 0, device_height_u: 1 }).success).toBe(false)
  })
  it('mergeEdit keeps untouched fields and nulls cleared ones', () => {
    expect(mergeEdit({ id: 's1', name: 'a', gateway: '10.0.0.1', tags: { x: '1' } }, { name: 'b', gateway: undefined }, ['name', 'gateway'])).toEqual({ id: 's1', name: 'b', gateway: null, tags: { x: '1' } })
  })
})

describe('rack elevation', () => {
  it('draws multi-U devices as merged cells, free units as buttons, and flags overlaps', async () => {
    const w = mount(Rack, { props: { sizeU: 6, devices: [dev({ id: 'a', name: 'srv-a', rack_position: 1, device_height_u: 2 }), dev({ id: 'b', name: 'srv-b', rack_position: 5, device_height_u: 1 }), dev({ id: 'c', name: 'loose' })] } })
    expect(w.find('[style]').exists()).toBe(false)
    const labels = w.findAll('ol > li > span').map((s) => s.text())
    expect(labels).toEqual(['6', '5', '4', '3', '2', '1'])
    expect(w.find('[data-test="rack-device-a"]').text()).toContain('2U')
    expect(w.findAll('[data-test^="rack-slot-"]').map((b) => b.attributes('data-test'))).toEqual(['rack-slot-6', 'rack-slot-4', 'rack-slot-3'])
    expect(w.text()).toContain('no position')
    await w.find('[data-test="rack-slot-4"]').trigger('click')
    expect(w.emitted('place')![0]).toEqual([4])
    await w.setProps({ devices: [dev({ id: 'a', name: 'srv-a', rack_position: 1, device_height_u: 2 }), dev({ id: 'b', name: 'srv-b', rack_position: 2 })] })
    expect(w.text()).toContain('2 devices overlap')
    expect(w.text()).toContain('srv-a / srv-b')
  })
})

describe('ipam CRUD views', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.cookie = '__Host-csrf=tok; Secure; Path=/'
    ;(globalThis as unknown as { __vw: number }).__vw = 1280
  })

  it('subnets: split previews with dry_run, then creates; add child pre-fills the parent', async () => {
    const parent = { id: 's1', name: 'Office', cidr: '10.0.0.0/24', prefix_length: 24, ip_version: 4, status: 'active' }
    const calls = fetchMock((url, init) => {
      if (url.includes('/split')) {
        const dry = body({ url, init }).dry_run
        return { parent, created: [{ cidr: '10.0.0.0/26', total_addresses: 64 }, { cidr: '10.0.0.64/26', total_addresses: 64 }], skipped: dry ? [{ cidr: '10.0.0.128/25', reason: 'overlaps 10.0.0.128/25' }] : null }
      }
      if (init.method === 'POST') return { ...body({ url, init }), id: 'new' }
      if (url.endsWith('/subnets/s1') || url.endsWith('/subnets/new')) return parent
      return { items: url.includes('/subnets') ? [parent] : [] }
    })
    const w = mount(Subnets, { global, attachTo: document.body })
    await flushPromises()
    await drawerAction(w, 's1', 'drawer-split')
    const preview = calls.find((c) => c.url.endsWith('/subnets/s1/split'))!
    expect(body(preview)).toEqual({ prefix_length: 25, dry_run: true })
    clickIn(drawerEl(), '4 × /26')
    await flushPromises()
    expect(body(calls.at(-1)!)).toEqual({ prefix_length: 26, dry_run: true })
    expect(drawerEl().textContent).toContain('Skipped (1)')
    clickIn(drawerEl(), 'Create 2 subnets')
    await flushPromises()
    const splits = calls.filter((c) => c.url.endsWith('/split'))
    expect(body(splits.at(-1)!)).toEqual({ prefix_length: 26, dry_run: false })
    // Back on the subnet's details once the split is done.
    expect(document.body.querySelector('[data-test="split-dialog"]')).toBeNull()
    expect(drawerEl().querySelector('[data-test=drawer-scan]')).not.toBeNull()

    ;(drawerEl().querySelector('[data-test=drawer-add-child]') as HTMLButtonElement).click()
    await flushPromises()
    expect(drawerEl().querySelector('h2')?.textContent).toContain('New subnet in 10.0.0.0/24')
    const field = (n: string, v: string) => { const el = drawerEl().querySelector<HTMLInputElement>(`input[data-field=${n}]`)!; el.value = v; el.dispatchEvent(new Event('input')) }
    field('name', 'Printers')
    field('cidr', '10.0.0.192/26')
    clickIn(drawerEl(), 'Save')
    await flushPromises()
    const post = calls.find((c) => c.init.method === 'POST' && c.url.endsWith('/api/ipam/v1/subnets'))!
    expect(body(post)).toMatchObject({ name: 'Printers', cidr: '10.0.0.192/26', parent_id: 's1', status: 'active' })
    w.unmount()
  })

  it('groups: create a group, add a member (type-checked), edit sends the whole member', async () => {
    const group = { id: 'g1', name: 'Web', status: 'active', member_count: 0 }
    const members = [{ id: 'm1', member_type: 'address', value: '10.0.0.5', sequence: 1 }]
    const calls = fetchMock((url, init) => {
      if (init.method === 'POST' || init.method === 'PUT') return { ...body({ url, init }), id: 'x' }
      if (url.includes('/ip-groups/g1/members')) return { items: members }
      if (url.includes('/ip-groups')) return { items: [group] }
      return { items: [] }
    })
    const w = mount(Groups, { global, attachTo: document.body })
    await flushPromises()
    await w.find('[data-test="group-new"]').trigger('click')
    await flushPromises()
    setField('name', 'DB servers')
    clickIn(dialog(), 'Save')
    await flushPromises()
    expect(body(calls.find((c) => c.init.method === 'POST' && c.url.endsWith('/ip-groups'))!)).toMatchObject({ name: 'DB servers', status: 'active' })

    await w.find('[data-test="ip-member-add-g1"]').trigger('click')
    await flushPromises()
    setField('member_type', 'range')
    setField('value', '10.0.0.10')
    clickIn(dialog(), 'Save')
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'POST' && c.url.endsWith('/members'))).toBe(false)
    setField('value', '10.0.0.10-10.0.0.20')
    clickIn(dialog(), 'Save')
    await flushPromises()
    expect(body(calls.find((c) => c.init.method === 'POST' && c.url.endsWith('/ip-groups/g1/members'))!)).toMatchObject({ member_type: 'range', value: '10.0.0.10-10.0.0.20', sequence: 2 })
    w.unmount()
  })

  it("groups: check IP reads the server's matching_groups", async () => {
    fetchMock((url) => (url.includes('/ip-groups/check') ? { matching_groups: [{ id: 'g1', name: 'Web', status: 'active' }] } : { items: [] }))
    const w = mount(Groups, { global, attachTo: document.body })
    await flushPromises()
    const ip = w.find<HTMLInputElement>('input[data-field=ip]')
    await ip.setValue('10.0.0.5')
    await w.findAll('button').find((b) => b.text() === 'Check')!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Web')
    expect(w.text()).not.toContain('No groups contain')
    w.unmount()
  })

  it('locations: a rack shows its devices; placing into a free unit PUTs the rack fields, collisions are refused', async () => {
    const rack = { id: 'r1', name: 'Rack A', location_type: 'rack', status: 'active', rack_size_u: 4 }
    const mounted = dev({ id: 'd1', name: 'sw-1', rack_id: 'r1', rack_position: 3, device_height_u: 2 })
    const spare = dev({ id: 'd2', name: 'srv-2' })
    const calls = fetchMock((url, init) => {
      if (init.method === 'PUT') return body({ url, init })
      if (url.includes('/locations/tree')) return { tree: [{ ...rack, children: [] }] }
      if (url.includes('/locations')) return { items: [rack] }
      if (url.includes('rack_id=r1')) return { items: [mounted] }
      if (url.includes('/devices')) return { items: [mounted, spare] }
      return { items: [] }
    })
    const w = mount(Locations, { global, attachTo: document.body })
    await flushPromises()
    await w.find('[role=treeitem]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="rack-device-d1"]').exists()).toBe(true)
    await w.find('[data-test="rack-slot-2"]').trigger('click')
    await flushPromises()
    setField('device_id', 'd2')
    setField('device_height_u', '2')
    clickIn(dialog(), 'Place')
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'PUT')).toBe(false)
    expect(dialog().textContent).toContain('Overlaps sw-1')
    setField('device_height_u', '1')
    clickIn(dialog(), 'Place')
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')!
    expect(put.url).toBe('/api/ipam/v1/devices/d2')
    expect(body(put)).toMatchObject({ id: 'd2', name: 'srv-2', rack_id: 'r1', rack_position: 2, device_height_u: 1 })
    w.unmount()
  })

  it('subnets: scan follows the queued job to completion, then reloads', async () => {
    vi.useFakeTimers()
    const parent = { id: 's1', name: 'Office', cidr: '10.0.0.0/24', prefix_length: 24, ip_version: 4, status: 'active' }
    let polls = 0
    const calls = fetchMock((url) => {
      if (url.endsWith('/subnets/s1/scan')) return { id: 'j1', subnet_id: 's1', status: 'pending', progress: 0 }
      if (url.endsWith('/subnets/s1')) return parent
      if (url.endsWith('/ip-scans/j1')) return ++polls < 2 ? { id: 'j1', subnet_id: 's1', status: 'scanning', progress: 40, scanned_count: 100, total_addresses: 254, alive_count: 3 } : { id: 'j1', subnet_id: 's1', status: 'completed', progress: 100, alive_count: 84, new_count: 84, updated_count: 0 }
      if (url.includes('/subnets/tree')) return { tree: [] }
      return { items: url.includes('/subnets') ? [parent] : [] }
    })
    const w = mount(Subnets, { global, attachTo: document.body })
    await flushPromises()
    await drawerAction(w, 's1', 'drawer-scan')
    const status = () => drawerEl().querySelector('[data-test=scan-status]')?.textContent ?? ''
    expect(status()).toContain('Scanning')
    await vi.advanceTimersByTimeAsync(1000)
    expect(status()).toContain('40% (100/254 probed, 3 alive)')
    const before = calls.filter((c) => c.url.includes('/api/ipam/v1/subnets?') || c.url.endsWith('/api/ipam/v1/subnets')).length
    await vi.advanceTimersByTimeAsync(1000)
    expect(status()).toContain('complete: 84 alive, 84 new')
    expect(calls.filter((c) => c.url.includes('/api/ipam/v1/subnets?') || c.url.endsWith('/api/ipam/v1/subnets')).length).toBeGreaterThan(before)
    vi.useRealTimers()
    w.unmount()
  })

  it('addresses: ping reports its result on the button', async () => {
    vi.stubGlobal('EventSource', class { onopen = null; onerror = null; addEventListener() {} close() {} })
    fetchMock((url) => (url.includes('/ping') ? { address: '10.0.0.1', alive: true, rtt_ms: 2, available: true } : url.includes('/subnets') ? { items: [] } : { items: [{ id: 'a1', address: '10.0.0.1', status: 'active', address_type: 'host' }, { id: 'a2', address: '10.0.0.2', status: 'active', address_type: 'host' }] }))
    const w = mount(Addresses, { global, attachTo: document.body })
    await flushPromises()
    await w.find('[data-test="address-ping-a1"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="address-ping-a1"]').text()).toBe('2 ms')
    expect(w.find('[data-test="address-ping-a2"]').text()).toBe('Ping')
    w.unmount()
  })
})
