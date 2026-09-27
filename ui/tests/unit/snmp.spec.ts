import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import { createRouter, createMemoryHistory } from 'vue-router'
import { useConfirm } from '@go-tangra/ui'
import SubnetSnmpCard from '@/components/SubnetSnmpCard.vue'
import Subnets from '@/views/subnets/index.vue'
import Scans from '@/views/scans/index.vue'
import { snmpPhaseText } from '@/views/scans/snmp'
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
const MANAGE = [{ action: 'configure', subject: 'SubnetSnmp' }]

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

  it('hides the form without the configure SubnetSnmp ability', async () => {
    fetchMock(() => ({ body: ownV2 }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility([{ action: 'read', subject: 'Subnet' }]) })
    await flushPromises()
    expect(w.find('[data-test=snmp-status]').text()).toContain('v2c')
    expect(w.find('[data-test=snmp-set]').exists()).toBe(false)
    expect(w.find('[data-test=snmp-readonly]').exists()).toBe(true)
    w.unmount()
  })
})

describe('snmp schema (v3)', () => {
  const base = { version: '3', user: 'lab', security_level: 'authNoPriv', auth_protocol: 'SHA256', auth_password: 'authpass1' }
  it('authNoPriv sends no privacy fields', () => {
    const r = snmpSchema.safeParse({ ...base, community: 'x', priv_protocol: 'AES', priv_password: 'privpass1' })
    expect(r.success && r.data).toEqual({ version: 3, user: 'lab', security_level: 'authNoPriv', auth_protocol: 'SHA256', auth_password: 'authpass1' })
  })
  it('authPriv requires every field and 8-character passwords', () => {
    const ok = snmpSchema.safeParse({ ...base, security_level: 'authPriv', priv_protocol: 'AES256', priv_password: 'privpass1' })
    expect(ok.success && ok.data).toEqual({ version: 3, user: 'lab', security_level: 'authPriv', auth_protocol: 'SHA256', auth_password: 'authpass1', priv_protocol: 'AES256', priv_password: 'privpass1' })
    for (const bad of [
      { ...base, security_level: 'authPriv', priv_protocol: 'AES256' },
      { ...base, security_level: 'authPriv', priv_password: 'privpass1' },
      { ...base, auth_password: 'short' },
      { ...base, user: '' },
      { ...base, auth_protocol: '' },
      { ...base, security_level: '' },
    ]) expect(snmpSchema.safeParse(bad).success).toBe(false)
  })
})

describe('SubnetSnmpCard (v3)', () => {
  it('marks weak protocols and submits authPriv', async () => {
    const calls = fetchMock((_url, init) => ({ body: init.method === 'PUT' ? { own: { version: 3, security_level: 'authPriv', weak: false }, effective: { state: 'own', version: 3, security_level: 'authPriv' } } : none }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    await w.find('[data-test=snmp-set]').trigger('click')
    const selects = () => w.findAll('[data-test=snmp-form] select')
    await selects()[0]!.setValue('3')
    expect(w.find('[data-test=snmp-form]').text()).not.toContain('Community')
    await selects()[1]!.setValue('authPriv')
    const opts = selects()[2]!.findAll('option').map((o) => o.text())
    expect(opts.some((t) => t.includes('MD5') && t.includes('weak'))).toBe(true)
    expect(opts.some((t) => t.includes('SHA-256') && !t.includes('weak'))).toBe(true)
    await selects()[2]!.setValue('SHA256')
    await selects()[3]!.setValue('AES256')
    const secrets = w.findAll('[data-test=snmp-form] input[type=password]')
    expect(secrets.length).toBe(3)
    await secrets[0]!.setValue('labuser')
    await secrets[1]!.setValue('authpass-1')
    await secrets[2]!.setValue('privpass-1')
    await w.find('[data-test=snmp-save]').trigger('click')
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')
    expect(JSON.parse(String(put?.init.body))).toEqual({ version: 3, user: 'labuser', security_level: 'authPriv', auth_protocol: 'SHA256', auth_password: 'authpass-1', priv_protocol: 'AES256', priv_password: 'privpass-1' })
    expect(w.html()).not.toContain('privpass-1')
    expect(w.find('[data-test=snmp-status]').text()).toContain('v3 authPriv')
    w.unmount()
  })
})

describe('SNMP inheritance in the UI', () => {
  it('the card names the subnet the credentials are inherited from', async () => {
    fetchMock(() => ({ body: { own: null, effective: { state: 'inherited', version: 3, security_level: 'authPriv', source_subnet_id: 'p1', source_name: 'supernet', source_cidr: '10.0.0.0/8' } } }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    const txt = w.find('[data-test=snmp-status]').text()
    expect(txt).toContain('Inherited from supernet (10.0.0.0/8)')
    expect(txt).toContain('v3 authPriv')
    expect(w.find('[data-test=snmp-set]').text()).toContain('Set')
    w.unmount()
  })

  it('the subnet list shows an SNMP badge per subnet', async () => {
    vi.stubGlobal('EventSource', class { onopen = null; onerror = null; addEventListener() {} close() {} })
    const rows = [
      { ...subnet, id: 'a', snmp: { state: 'own', version: 2 } },
      { ...subnet, id: 'b', cidr: '10.1.112.0/25', parent_id: 'a', snmp: { state: 'inherited', version: 2, source_name: 'mgmt', source_cidr: '10.1.112.0/24' } },
      { ...subnet, id: 'c', cidr: '192.168.0.0/24', snmp: { state: 'none' } },
    ]
    fetchMock((url) => ({ body: url.includes('/subnets/tree') ? { tree: [] } : { items: rows } }))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }] })
    const w = mount(Subnets, { global: { plugins: [router, [abilitiesPlugin, createMongoAbility([]), { useGlobalProperties: true }]] as never }, attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=subnet-row-a] [data-test=snmp-badge]').text()).toContain('v2c')
    expect(w.find('[data-test=subnet-row-b] [data-test=snmp-badge]').text()).toContain('inherited')
    expect(w.find('[data-test=subnet-row-b] [data-test=snmp-badge]').attributes('title')).toContain('mgmt (10.1.112.0/24)')
    expect(w.find('[data-test=subnet-row-c] [data-test=snmp-badge]').exists()).toBe(false)
    w.unmount()
  })
})

describe('Test SNMP', () => {
  it('probes an address and shows the outcome', async () => {
    const calls = fetchMock((url, init) => {
      if (init.method === 'POST') {
        const addr = JSON.parse(String(init.body)).address
        return addr === '10.1.112.20'
          ? { body: { outcome: 'ok', sys_name: 'sw-core-1', sys_descr: 'Cisco IOS', source_subnet_id: 's1', duration_ms: 42 } }
          : { body: { outcome: 'auth_failed', source_subnet_id: 's1', duration_ms: 12 } }
      }
      return { body: ownV2 }
    })
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility([{ action: 'test', subject: 'SubnetSnmp' }]) })
    await flushPromises()
    await w.find('[data-test=snmp-test-address] input').setValue('10.1.112.20')
    await w.find('[data-test=snmp-test]').trigger('click')
    await flushPromises()
    const post = calls.find((c) => c.init.method === 'POST')
    expect(post?.url).toContain('/subnets/s1/snmp/test')
    expect(w.find('[data-test=snmp-test-result]').text()).toContain('sw-core-1')
    await w.find('[data-test=snmp-test-address] input').setValue('10.1.112.21')
    await w.find('[data-test=snmp-test]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test=snmp-test-result]').text()).toContain('rejected')
    w.unmount()
  })

  it('validates the address locally and hides the test without scan:run', async () => {
    const calls = fetchMock(() => ({ body: ownV2 }))
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility([{ action: 'test', subject: 'SubnetSnmp' }]) })
    await flushPromises()
    await w.find('[data-test=snmp-test-address] input').setValue('not-an-ip')
    await w.find('[data-test=snmp-test]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'POST')).toBe(false)
    w.unmount()
    const r = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    expect(r.find('[data-test=snmp-test]').exists()).toBe(false)
    r.unmount()
  })
})

describe('scan SNMP phase', () => {
  it('states a reason for every SNMP outcome', () => {
    const label = (id: string) => (id === 'p1' ? '10.0.0.0/8' : id)
    const base = { id: 'j', subnet_id: 's1', status: 'completed' as const, progress: 100 }
    expect(snmpPhaseText({ ...base, snmp_status: 'not_requested' }, label)).toBe('not requested')
    expect(snmpPhaseText({ ...base, enable_snmp: true, snmp_status: 'no_credentials' }, label)).toContain('no credentials')
    expect(snmpPhaseText({ ...base, enable_snmp: true, snmp_status: 'credentials_unreadable' }, label)).toContain('unreadable')
    expect(snmpPhaseText({ ...base, enable_snmp: true, snmp_status: 'no_live_hosts' }, label)).toContain('no live hosts')
    const ran = snmpPhaseText({ ...base, enable_snmp: true, snmp_status: 'ran', snmp_source_subnet_id: 'p1', snmp_probed: 5, snmp_discovered_count: 0, snmp_no_answer: 3, snmp_rejected: 2 }, label)
    expect(ran).toContain('inherited from 10.0.0.0/8')
    expect(ran).toContain('probed 5')
    expect(ran).toContain('discovered 0')
    expect(ran).toContain('rejected 2')
    expect(snmpPhaseText({ ...base, enable_snmp: true, snmp_status: 'ran', snmp_source_subnet_id: 's1', snmp_probed: 1, snmp_discovered_count: 1 }, label)).toContain('own credentials')
    expect(snmpPhaseText({ ...base, status: 'scanning', enable_snmp: true }, label)).toBe('')
  })

  it('the scans table shows the SNMP phase per job', async () => {
    vi.stubGlobal('EventSource', class { onopen = null; onerror = null; addEventListener() {} close() {} })
    fetchMock((url) => ({ body: url.includes('/ip-scans') ? { items: [{ id: 'j1', subnet_id: 's1', status: 'completed', progress: 100, enable_snmp: true, snmp_status: 'no_credentials' }] } : { items: [subnet] } }))
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }] })
    const w = mount(Scans, { global: { plugins: [router, [abilitiesPlugin, createMongoAbility([]), { useGlobalProperties: true }]] as never }, attachTo: document.body })
    await flushPromises()
    expect(w.find('[data-test=scan-row-j1] [data-test=snmp-phase]').text()).toContain('no credentials')
    w.unmount()
  })
})

describe('Clear SNMP credentials', () => {
  it('asks through the kit confirm dialog and deletes only when confirmed', async () => {
    let status: SubnetSNMPStatus = ownV2
    const calls = fetchMock((_url, init) => {
      if (init.method === 'DELETE') {
        status = none
        return { status: 204, body: null }
      }
      return { body: status }
    })
    const confirm = useConfirm()
    const w = mount(SubnetSnmpCard, { props: { subnet }, global: withAbility(MANAGE) })
    await flushPromises()
    await w.find('[data-test=snmp-clear]').trigger('click')
    expect(confirm.state.pending?.title).toContain('Clear')
    confirm.answer(false)
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'DELETE')).toBe(false)
    await w.find('[data-test=snmp-clear]').trigger('click')
    confirm.answer(true)
    await flushPromises()
    expect(calls.find((c) => c.init.method === 'DELETE')?.url).toContain('/subnets/s1/snmp')
    expect(w.find('[data-test=snmp-status]').text()).toContain('Not configured')
    expect(w.find('[data-test=snmp-clear]').exists()).toBe(false)
    w.unmount()
  })
})
