// Feature 032 (server-side tables): the IPAM tables page and sort on the
// server; live address / scan events reload the shown page (debounced) and
// never prepend rows.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import Addresses from '@/views/addresses/index.vue'
import Scans from '@/views/scans/index.vue'
import Vlans from '@/views/vlans/index.vue'
import Groups from '@/views/groups/index.vue'
import { RELOAD_MS, useLive } from '@/stores/live'
import { fetchAll } from '@/api/list'

type Call = { url: string; init: RequestInit }
function fetchMock(handler: (url: string, init: RequestInit) => unknown) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    return new Response(JSON.stringify(handler(url, init) ?? {}), { status: 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
class FakeSource { onopen = null; onerror = null; addEventListener() {} close() {} }
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }] })
const global = { plugins: [router, [abilitiesPlugin, createMongoAbility([{ action: 'manage', subject: 'all' }]), { useGlobalProperties: true }]] as never }
const params = (url: string) => new URL(url, 'https://x').searchParams
const header = (w: ReturnType<typeof mount>, label: string) => w.findAll('th button').find((b) => b.text().startsWith(label))
const subnet = { id: 's1', name: 'Office', cidr: '10.0.0.0/24', ip_version: 4, status: 'active' }

// A server of `total` addresses that echoes the request and clamps the page.
function addressServer(total = 130) {
  return (url: string) => {
    if (url.includes('/subnets')) return { items: [subnet], total: 1, page: 1, page_size: 200 }
    if (!url.startsWith('/api/ipam/v1/ip-addresses?')) return { items: [] }
    const q = params(url)
    const size = Number(q.get('page_size'))
    const page = Math.min(Number(q.get('page')), Math.max(1, Math.ceil(total / size)))
    return { items: [{ id: 'a' + page, address: '10.0.0.' + page, subnet_id: 's1', status: 'active', address_type: 'host' }], total, page, page_size: size, sort: q.get('sort'), order: q.get('order') }
  }
}

describe('ipam tables on the list contract', () => {
  beforeEach(async () => {
    await router.push('/') // page / size / sort live in the URL: start clean
    setActivePinia(createPinia())
    document.cookie = '__Host-csrf=tok; Secure; Path=/'
    vi.stubGlobal('EventSource', FakeSource)
    ;(globalThis as unknown as { __vw: number }).__vw = 1280
  })
  afterEach(() => vi.useRealTimers())

  it('addresses: pager with the total, inet address order by default, whole-list header sort, filters back to page 1', async () => {
    const calls = fetchMock(addressServer())
    const w = mount(Addresses, { global, attachTo: document.body })
    await flushPromises()
    const lists = () => calls.filter((c) => c.url.startsWith('/api/ipam/v1/ip-addresses?'))
    const last = () => params(lists().at(-1)!.url)
    expect(lists()[0]!.url).toBe('/api/ipam/v1/ip-addresses?page=1&page_size=25&sort=address&order=asc')
    expect(w.text()).toContain('Showing 1–25 of 130')
    await w.find('[aria-label="Page 3"]').trigger('click')
    await flushPromises()
    expect(last().get('page')).toBe('3')
    // Sortable headers are the server's fields; MAC sorts as `mac`.
    for (const label of ['Address', 'Hostname', 'MAC', 'Type', 'Status']) expect(header(w, label), label).toBeTruthy()
    await header(w, 'MAC')!.trigger('click')
    await flushPromises()
    expect([last().get('sort'), last().get('order'), last().get('page')]).toEqual(['mac', 'asc', '1'])
    await header(w, 'MAC')!.trigger('click')
    await flushPromises()
    expect(last().get('order')).toBe('desc')
    // The hostname filter reaches the server (it was ignored before 032) and
    // returns to page 1 keeping the sort.
    await w.find('[aria-label="Page 2"]').trigger('click')
    await flushPromises()
    const host = w.find('input[data-field=hostname]')
    await host.setValue('web')
    await host.trigger('keyup', { key: 'Enter' })
    await flushPromises()
    expect([last().get('hostname'), last().get('page'), last().get('sort'), last().get('order')]).toEqual(['web', '1', 'mac', 'desc'])
    w.unmount()
  })

  it('addresses: live events reload the shown page once per burst and never prepend', async () => {
    vi.useFakeTimers()
    const calls = fetchMock(addressServer(3))
    const w = mount(Addresses, { global, attachTo: document.body })
    await flushPromises()
    const lists = () => calls.filter((c) => c.url.startsWith('/api/ipam/v1/ip-addresses?'))
    expect(lists()).toHaveLength(1)
    const live = useLive()
    const created = JSON.stringify({ data: { id: 'new', address: '10.0.0.99', status: 'active', address_type: 'host' } })
    live._emit('ipam.ip_address.created', created)
    live._emit('ipam.ip_address.updated', created)
    live._emit('ipam.ip_address.deleted', JSON.stringify({ data: { id: 'a1' } }))
    await flushPromises()
    // Nothing is patched in: the row is not on the page until the server says so.
    expect(w.find('[data-test="address-row-new"]').exists()).toBe(false)
    expect(lists()).toHaveLength(1)
    vi.advanceTimersByTime(RELOAD_MS)
    await flushPromises()
    expect(lists()).toHaveLength(2) // three events, one reload of the same page
    expect(lists()[1]!.url).toBe(lists()[0]!.url)
    expect(w.find('[data-test="address-row-new"]').exists()).toBe(false)
    // Scan events do not reload the address page.
    live._emit('ipam.scan.completed', JSON.stringify({ data: { job_id: 'j1', status: 'completed' } }))
    vi.advanceTimersByTime(RELOAD_MS)
    await flushPromises()
    expect(lists()).toHaveLength(2)
    w.unmount()
  })

  it('scans: newest first, live progress reloads the page instead of patching rows', async () => {
    vi.useFakeTimers()
    let progress = 10
    const calls = fetchMock((url) => (url.includes('/subnets') ? { items: [subnet] } : url.startsWith('/api/ipam/v1/ip-scans?') ? { items: [{ id: 'j0', subnet_id: 's1', status: 'scanning', progress }], total: 1, page: 1, page_size: 25, sort: 'created_at', order: 'desc' } : { items: [] }))
    const w = mount(Scans, { global, attachTo: document.body })
    await flushPromises()
    const lists = () => calls.filter((c) => c.url.startsWith('/api/ipam/v1/ip-scans?'))
    expect(lists()[0]!.url).toBe('/api/ipam/v1/ip-scans?page=1&page_size=25&sort=created_at&order=desc')
    for (const label of ['Created', 'Subnet', 'Status']) expect(header(w, label), label).toBeTruthy()
    expect(w.find('[data-test="scan-row-j0"]').text()).toContain('10%')
    progress = 60
    useLive()._emit('ipam.scan.started', JSON.stringify({ data: { job_id: 'j9', status: 'scanning', progress: 1 } }))
    await flushPromises()
    expect(w.find('[data-test="scan-row-j9"]').exists()).toBe(false) // never prepended
    vi.advanceTimersByTime(RELOAD_MS)
    await flushPromises()
    expect(lists()).toHaveLength(2)
    expect(w.find('[data-test="scan-row-j0"]').text()).toContain('60%')
    w.unmount()
  })

  it('a table that was never listed is not reloaded by live events', async () => {
    vi.useFakeTimers()
    const calls = fetchMock(() => ({ items: [] }))
    useLive()._emit('ipam.ip_address.created', JSON.stringify({ data: { id: 'x' } }))
    vi.advanceTimersByTime(RELOAD_MS)
    await flushPromises()
    expect(calls).toHaveLength(0)
  })

  it('vlans: sortable columns map to the server fields; page size changes are sent', async () => {
    const calls = fetchMock((url) => (url.startsWith('/api/ipam/v1/vlans?') ? { items: [{ id: 'v1', vlan_id: 10, name: 'users', status: 'active' }], total: 60, page: 1, page_size: 25, sort: 'vlan_id', order: 'asc' } : { items: [] }))
    const w = mount(Vlans, { global, attachTo: document.body })
    await flushPromises()
    const lists = () => calls.filter((c) => c.url.startsWith('/api/ipam/v1/vlans?') && params(c.url).get('page_size') !== '200')
    expect(lists()[0]!.url).toBe('/api/ipam/v1/vlans?page=1&page_size=25&sort=vlan_id&order=asc')
    await header(w, 'Domain')!.trigger('click')
    await flushPromises()
    expect([params(lists().at(-1)!.url).get('sort'), params(lists().at(-1)!.url).get('order')]).toEqual(['domain', 'asc'])
    const size = w.findAll('select').find((s) => s.findAll('option').some((o) => o.text() === '50'))!
    await size.setValue('50')
    await flushPromises()
    expect(params(lists().at(-1)!.url).get('page_size')).toBe('50')
    w.unmount()
  })

  it('groups: member tables page and sort on the server; the next order number comes from the highest member', async () => {
    const calls = fetchMock((url) => {
      if (url.startsWith('/api/ipam/v1/ip-groups?') || url === '/api/ipam/v1/ip-groups') return { items: [{ id: 'g1', name: 'web', status: 'active', member_count: 40 }] }
      if (url.includes('/ip-groups/g1/members?')) {
        const q = params(url)
        if (q.get('page_size') === '1') return { items: [{ id: 'm40', ip_group_id: 'g1', member_type: 'address', value: '10.0.0.40', sequence: 40 }], total: 40, page: 1, page_size: 1, sort: 'sequence', order: 'desc' }
        return { items: [{ id: 'm' + q.get('page'), ip_group_id: 'g1', member_type: 'address', value: '10.0.0.1', sequence: 1 }], total: 40, page: Number(q.get('page')), page_size: 25, sort: q.get('sort'), order: q.get('order') }
      }
      return { items: [] }
    })
    const w = mount(Groups, { global, attachTo: document.body })
    await flushPromises()
    const group = w.find('details')
    ;(group.element as HTMLDetailsElement).open = true
    await group.trigger('toggle')
    await flushPromises()
    const members = () => calls.filter((c) => c.url.includes('/ip-groups/g1/members?'))
    expect(members()[0]!.url).toBe('/api/ipam/v1/ip-groups/g1/members?page=1&page_size=25&sort=sequence&order=asc')
    expect(w.text()).toContain('Showing 1–25 of 40')
    await w.find('[aria-label="Page 2"]').trigger('click')
    await flushPromises()
    expect(params(members().at(-1)!.url).get('page')).toBe('2')
    await header(w, 'Value')!.trigger('click')
    await flushPromises()
    expect([params(members().at(-1)!.url).get('sort'), params(members().at(-1)!.url).get('page')]).toEqual(['name', '1'])
    await w.find('[data-test=ip-member-add-g1]').trigger('click')
    await flushPromises()
    const seq = document.body.querySelector<HTMLInputElement>('[role=dialog] input[data-field=sequence]')!
    expect(seq.value).toBe('41')
    w.unmount()
  })

  it('fetchAll walks every page at the largest size and stops at the total', async () => {
    const calls = fetchMock((url) => {
      const page = Number(params(url).get('page'))
      return { items: Array.from({ length: page < 3 ? 200 : 5 }, (_, i) => ({ id: `${page}-${i}` })), total: 405, page, page_size: 200 }
    })
    const all = await fetchAll<{ id: string }>('devices', { status: 'active' })
    expect(all).toHaveLength(405)
    expect(calls.map((c) => params(c.url).get('page'))).toEqual(['1', '2', '3'])
    expect(params(calls[0]!.url).get('status')).toBe('active')
  })
})
