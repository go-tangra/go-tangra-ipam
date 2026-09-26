import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import SubnetSnmpCard from '@/components/SubnetSnmpCard.vue'
import { snmpSchema } from '@/schemas'
import type { Subnet, SubnetSNMPStatus } from '@/api/types'

// Feature 021: the write-only SNMP credential card of the subnet drawer.

type Handler = (url: string, init: RequestInit) => { status?: number; body: unknown }
function fetchMock(handler: Handler) {
  const calls: { url: string; init: RequestInit }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    calls.push({ url, init })
    const r = handler(url, init)
    return new Response(r.status === 204 ? null : JSON.stringify(r.body), { status: r.status ?? 200, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}
const withAbility = (rules: { action: string; subject: string }[]) => ({ plugins: [[abilitiesPlugin, createMongoAbility(rules), { useGlobalProperties: true }]] as never })
const MANAGE = [{ action: 'manage', subject: 'SubnetSnmp' }]

const subnet: Subnet = { id: 's1', name: 'mgmt', cidr: '10.1.112.0/24', status: 'active', ip_version: 4 }
const none: SubnetSNMPStatus = { own: null, effective: { state: 'none' } }
const ownV2: SubnetSNMPStatus = { own: { version: 2, weak: false, updated_at: '2026-09-27T10:00:00Z' }, effective: { state: 'own', version: 2, source_subnet_id: 's1', source_name: 'mgmt', source_cidr: '10.1.112.0/24' } }

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})

describe('snmp schema', () => {
  it('accepts v2c with a community and sends only the community', () => {
    const r = snmpSchema.safeParse({ version: '2', community: 'pub lic', user: 'x', auth_password: 'ignored1' })
    expect(r.success && r.data).toEqual({ version: 2, community: 'pub lic' })
  })
  it('rejects an empty or too long community', () => {
    expect(snmpSchema.safeParse({ version: '2', community: '' }).success).toBe(false)
    expect(snmpSchema.safeParse({ version: '2', community: 'x'.repeat(257) }).success).toBe(false)
  })
})

describe('SubnetSnmpCard (v2c)', () => {
  it('shows the status and never a credential value', async () => {
    fetchMock(() => ({ body: ownV2 }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    expect(w.find('[data-test=snmp-status]').text()).toContain('v2c')
    expect(w.find('[data-test=snmp-status]').text()).toContain('Own')
    expect(w.find('[data-test=snmp-set]').text()).toContain('Replace')
    w.unmount()
  })

  it('submits v2c credentials, then clears the field and hides the form', async () => {
    let status = none
    const calls = fetchMock((_url, init) => {
      if (init.method === 'PUT') status = ownV2
      return { body: status }
    })
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    expect(w.find('[data-test=snmp-status]').text()).toContain('Not configured')
    await w.find('[data-test=snmp-set]').trigger('click')
    const input = w.find('[data-test=snmp-form] input[type=password]')
    await input.setValue('c0mm-S3CRET')
    await w.find('[data-test=snmp-save]').trigger('click')
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')
    expect(put?.url).toContain('/subnets/s1/snmp')
    expect(JSON.parse(String(put?.init.body))).toEqual({ version: 2, community: 'c0mm-S3CRET' })
    expect(w.find('[data-test=snmp-form]').exists()).toBe(false)
    expect(w.html()).not.toContain('c0mm-S3CRET')
    expect(w.find('[data-test=snmp-status]').text()).toContain('v2c')
    // Reopening the form never pre-fills the old value.
    await w.find('[data-test=snmp-set]').trigger('click')
    expect((w.find('[data-test=snmp-form] input[type=password]').element as HTMLInputElement).value).toBe('')
    w.unmount()
  })

  it('shows the server field error', async () => {
    fetchMock((_url, init) => (init.method === 'PUT' ? { status: 422, body: { reason: 'validation_failed', detail: { field: 'community', message: 'too long', fields: { community: 'too long' } } } } : { body: none }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    await w.find('[data-test=snmp-set]').trigger('click')
    await w.find('[data-test=snmp-form] input[type=password]').setValue('abc')
    await w.find('[data-test=snmp-save]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test=snmp-form]').text()).toContain('too long')
    w.unmount()
  })

  it('hides the form without the manage SubnetSnmp ability', async () => {
    fetchMock(() => ({ body: ownV2 }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility([{ action: 'read', subject: 'Subnet' }]) })
    await flushPromises()
    expect(w.find('[data-test=snmp-status]').text()).toContain('v2c')
    expect(w.find('[data-test=snmp-set]').exists()).toBe(false)
    expect(w.find('[data-test=snmp-readonly]').exists()).toBe(true)
    w.unmount()
  })
})
