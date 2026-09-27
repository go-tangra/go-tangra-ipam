import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import { useConfirm } from '@go-tangra/ui'
import Vlans from '@/views/vlans/index.vue'

type Call = { url: string; init: RequestInit }
type Reply = { status?: number; body?: unknown }
function fetchMock(handler: (url: string, init: RequestInit) => Reply) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    const r = handler(url, init)
    const status = r.status ?? 200
    return new Response(status === 204 ? null : JSON.stringify(r.body ?? {}), { status, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
const withAbility = (rules: { action: string; subject: string }[]) => ({ plugins: [[abilitiesPlugin, createMongoAbility(rules), { useGlobalProperties: true }]] as never })
const READ = [{ action: 'read', subject: 'Vlan' }]
const MANAGE = [...READ, { action: 'create', subject: 'Vlan' }, { action: 'update', subject: 'Vlan' }, { action: 'delete', subject: 'Vlan' }]
const body = (c: Call) => JSON.parse(String(c.init.body))
const dialog = () => document.body.querySelector('[role=dialog]')!
function setField(name: string, value: string): void {
  const el = dialog().querySelector<HTMLInputElement | HTMLSelectElement>(`input[data-field=${name}], select[data-field=${name}]`)!
  el.value = value
  el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input'))
}
function clickIn(root: ParentNode, text: string): void {
  const b = Array.from(root.querySelectorAll('button')).find((x) => x.textContent?.trim().startsWith(text))
  if (!b) throw new Error('no button ' + text)
  ;(b as HTMLButtonElement).click()
}
const users = { id: 'v1', vlan_id: 10, name: 'Users', domain: 'hq', status: 'active', description: 'office floor', subnet_count: 0 }
const voice = { id: 'v2', vlan_id: 20, name: 'Voice', status: 'active', subnet_count: 2 }

describe('VLAN management', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.cookie = '__Host-csrf=tok; Secure; Path=/'
    ;(globalThis as unknown as { __vw: number }).__vw = 1280
  })

  it('read-only callers see the list but no New / edit / delete controls', async () => {
    fetchMock(() => ({ body: { items: [users] } }))
    const w = mount(Vlans, { global: withAbility(READ), attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test="vlan-row-v1"]').exists()).toBe(true)
    expect(w.find('[data-test=vlan-new]').exists()).toBe(false)
    expect(w.find('[data-test=vlan-edit-v1]').exists()).toBe(false)
    expect(w.find('[data-test=vlan-delete-v1]').exists()).toBe(false)
    await w.find('[data-test="vlan-row-v1"]').trigger('click')
    await flushPromises()
    expect(document.body.querySelector('[role=dialog]')).toBeNull()
    w.unmount()
  })

  it('create posts the form and reloads the list', async () => {
    const calls = fetchMock((url, init) => (init.method === 'POST' ? { status: 201, body: { ...body({ url, init }), id: 'v9', subnet_count: 0 } } : { body: { items: [users] } }))
    const w = mount(Vlans, { global: withAbility(MANAGE), attachTo: document.body })
    await flushPromises()
    await w.find('[data-test=vlan-new]').trigger('click')
    await flushPromises()
    setField('vlan_id', '30')
    setField('name', 'Printers')
    const lists = calls.filter((c) => !c.init.method || c.init.method === 'GET').length
    clickIn(dialog(), 'Save')
    await flushPromises()
    const post = calls.find((c) => c.init.method === 'POST')!
    expect(post.url).toBe('/api/ipam/v1/vlans')
    expect(body(post)).toMatchObject({ vlan_id: 30, name: 'Printers', status: 'active' })
    expect(calls.filter((c) => !c.init.method || c.init.method === 'GET').length).toBeGreaterThan(lists)
    expect(document.body.querySelector('[role=dialog]')).toBeNull()
    w.unmount()
  })

  it('a duplicate VLAN id is shown on the field, not as a generic error', async () => {
    const calls = fetchMock((_url, init) => (init.method === 'POST' ? { status: 409, body: { reason: 'conflict' } } : { body: { items: [users] } }))
    const w = mount(Vlans, { global: withAbility(MANAGE), attachTo: document.body })
    await flushPromises()
    await w.find('[data-test=vlan-new]').trigger('click')
    await flushPromises()
    setField('vlan_id', '10')
    setField('name', 'Guests')
    clickIn(dialog(), 'Save')
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'POST')).toBe(true)
    expect(dialog().textContent).toContain('VLAN 10 already exists (Users, hq).')
    w.unmount()
  })

  it('row click opens the edit drawer; save PUTs the whole record', async () => {
    const calls = fetchMock((url, init) => (init.method === 'PUT' ? { body: body({ url, init }) } : { body: { items: [users] } }))
    const w = mount(Vlans, { global: withAbility(MANAGE), attachTo: document.body })
    await flushPromises()
    await w.find('[data-test="vlan-row-v1"]').trigger('click')
    await flushPromises()
    expect(dialog().textContent).toContain('Edit VLAN 10')
    setField('name', 'Staff')
    setField('domain', '')
    clickIn(dialog(), 'Save')
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')!
    expect(put.url).toBe('/api/ipam/v1/vlans/v1')
    // Untouched fields (description) go back; a cleared domain goes out as null.
    expect(body(put)).toMatchObject({ id: 'v1', vlan_id: 10, name: 'Staff', domain: null, status: 'active', description: 'office floor' })
    w.unmount()
  })

  it('delete asks through the kit confirm dialog; a VLAN with subnets is deleted with force', async () => {
    const calls = fetchMock((_url, init) => (init.method === 'DELETE' ? { status: 204 } : { body: { items: [users, voice] } }))
    const confirm = useConfirm()
    const w = mount(Vlans, { global: withAbility(MANAGE), attachTo: document.body })
    await flushPromises()
    await w.find('[data-test=vlan-delete-v1]').trigger('click')
    expect(confirm.state.pending?.title).toBe('Delete VLAN 10 (Users)?')
    confirm.answer(false)
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'DELETE')).toBe(false)

    await w.find('[data-test=vlan-delete-v1]').trigger('click')
    confirm.answer(true)
    await flushPromises()
    expect(calls.find((c) => c.init.method === 'DELETE')!.url).toBe('/api/ipam/v1/vlans/v1?force=false')

    await w.find('[data-test=vlan-delete-v2]').trigger('click')
    expect(confirm.state.pending?.text).toContain('2 subnets are bound to it')
    confirm.answer(true)
    await flushPromises()
    expect(calls.filter((c) => c.init.method === 'DELETE').at(-1)!.url).toBe('/api/ipam/v1/vlans/v2?force=true')
    w.unmount()
  })

  it('a refused delete is shown on the page', async () => {
    fetchMock((_url, init) => (init.method === 'DELETE' ? { status: 403, body: { reason: 'forbidden' } } : { body: { items: [users] } }))
    const confirm = useConfirm()
    const w = mount(Vlans, { global: withAbility(MANAGE), attachTo: document.body })
    await flushPromises()
    await w.find('[data-test=vlan-delete-v1]').trigger('click')
    confirm.answer(true)
    await flushPromises()
    expect(w.find('[data-test=vlan-error]').text()).toBe('You are not allowed to do that.')
    w.unmount()
  })
})
