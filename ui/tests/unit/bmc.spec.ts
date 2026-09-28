import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import { useConfirm } from '@go-tangra/ui'
import DeviceBmcCard from '@/components/DeviceBmcCard.vue'
import WardenSecretPicker from '@/components/WardenSecretPicker.vue'
import type { BmcStatus } from '@/api/types'

// Feature 024: BMC credentials are a Warden secret reference; the picker lists
// secrets through Warden's own API, never requesting a password.

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
const MANAGE = [{ action: 'configure', subject: 'DeviceBmc' }]
const REF = '01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f'

const none: BmcStatus = { configured: false, ready: false, reason: 'bmc_not_configured', address: '10.1.112.14', address_source: 'reported' }
const ready: BmcStatus = { configured: true, reference: REF, access: 'ok', secret: { name: 'zax-5 IPMI', username: 'ADMIN', folder_path: '/infra/bmc' }, address: '10.1.112.14', address_source: 'reported', ready: true }
const secrets = { items: [{ id: REF, name: 'zax-5 IPMI', username: 'ADMIN', folder_path: '/infra/bmc', permissions: {}, metadata: {} }] }

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})

describe('DeviceBmcCard', () => {
  it('shows the Warden secret, username, folder and the reported BMC address', async () => {
    fetchMock(() => ({ body: ready }))
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility(MANAGE) })
    await flushPromises()
    const text = w.find('[data-test=bmc-status]').text()
    expect(text).toContain('zax-5 IPMI')
    expect(text).toContain('ADMIN')
    expect(text).toContain('/infra/bmc')
    expect(w.find('[data-test=bmc-address]').text()).toContain('10.1.112.14')
    expect(w.find('[data-test=bmc-address]').text()).toContain('reported by the agent')
    expect(w.find('[data-test=bmc-attach]').text()).toContain('Change')
    expect(w.emitted('status')?.[0]?.[0]).toEqual(ready)
    w.unmount()
  })

  it.each([
    [{ ...none }, 'No BMC credentials configured'],
    [{ configured: true, reference: REF, access: 'forbidden', ready: false, reason: 'bmc_secret_forbidden', address: '10.0.0.9', address_source: 'management_ip' }, 'no access'],
    [{ configured: true, reference: REF, access: 'not_found', ready: false, reason: 'bmc_secret_not_found' }, 'no longer exists'],
    [{ configured: true, reference: REF, access: 'unavailable', ready: false, reason: 'warden_unavailable' }, 'Warden is unavailable'],
  ] as [BmcStatus, string][])('explains %#', async (st, text) => {
    fetchMock(() => ({ body: st }))
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility(MANAGE) })
    await flushPromises()
    expect(w.find('[data-test=bmc-status]').text()).toContain(text)
    w.unmount()
  })

  it('explains a missing BMC address', async () => {
    fetchMock(() => ({ body: { ...ready, address: undefined, address_source: undefined, ready: false, reason: 'bmc_no_address' } }))
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility(MANAGE) })
    await flushPromises()
    expect(w.find('[data-test=bmc-address]').text()).toContain('No BMC address')
    w.unmount()
  })

  it('attaches a secret picked from Warden and never asks Warden for a password', async () => {
    let status: BmcStatus = none
    const calls = fetchMock((url, init) => {
      if (url.startsWith('/api/warden/v1/')) return { body: secrets }
      if (init.method === 'PUT') {
        status = ready
        return { body: ready }
      }
      return { body: status }
    })
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility(MANAGE) })
    await flushPromises()
    await w.find('[data-test=bmc-attach]').trigger('click')
    await flushPromises()
    // The picker opens on the readable root secrets.
    expect(calls.some((c) => c.url.startsWith('/api/warden/v1/secrets?') && c.url.includes('root=true'))).toBe(true)
    await w.find('[data-test=warden-query] input').setValue('zax')
    await w.find('[data-test=warden-search]').trigger('click')
    await flushPromises()
    expect(calls.some((c) => c.url.startsWith('/api/warden/v1/secrets/search?') && c.url.includes('q=zax'))).toBe(true)
    await w.find('[data-test=warden-secret]').trigger('click')
    await flushPromises()
    const put = calls.find((c) => c.init.method === 'PUT')
    expect(put?.url).toBe('/api/ipam/v1/devices/d1/bmc')
    expect(JSON.parse(String(put?.init.body))).toEqual({ reference: REF })
    expect(w.find('[data-test=warden-picker]').exists()).toBe(false)
    expect(w.find('[data-test=bmc-status]').text()).toContain('zax-5 IPMI')
    expect(w.emitted('changed')).toBeTruthy()
    expect(calls.some((c) => c.url.includes('/password'))).toBe(false)
    w.unmount()
  })

  it('shows the refusal when the secret cannot be read', async () => {
    fetchMock((url, init) => {
      if (url.startsWith('/api/warden/v1/')) return { body: secrets }
      if (init.method === 'PUT') return { status: 403, body: { reason: 'bmc_secret_forbidden' } }
      return { body: none }
    })
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility(MANAGE) })
    await flushPromises()
    await w.find('[data-test=bmc-attach]').trigger('click')
    await flushPromises()
    await w.find('[data-test=warden-secret]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test=bmc-error]').text()).toContain('no access')
    w.unmount()
  })

  it('clears after confirmation', async () => {
    const calls = fetchMock((_url, init) => (init.method === 'DELETE' ? { status: 204, body: null } : { body: ready }))
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility(MANAGE) })
    await flushPromises()
    const confirm = useConfirm()
    await w.find('[data-test=bmc-clear]').trigger('click')
    await flushPromises()
    expect(confirm.state.pending?.title).toContain('Clear')
    confirm.answer(true)
    await flushPromises()
    expect(calls.some((c) => c.init.method === 'DELETE' && c.url === '/api/ipam/v1/devices/d1/bmc')).toBe(true)
    w.unmount()
  })

  it('is read-only without the configure DeviceBmc ability', async () => {
    fetchMock(() => ({ body: ready }))
    const w = mount(DeviceBmcCard, { props: { deviceId: 'd1' }, global: withAbility([{ action: 'control', subject: 'Power' }]) })
    await flushPromises()
    expect(w.find('[data-test=bmc-attach]').exists()).toBe(false)
    expect(w.find('[data-test=bmc-clear]').exists()).toBe(false)
    expect(w.find('[data-test=bmc-readonly]').exists()).toBe(true)
    w.unmount()
  })
})

describe('WardenSecretPicker', () => {
  it.each([
    [403, 'cannot read Warden secrets'],
    [404, 'Warden is unavailable'],
    [503, 'Warden is unavailable'],
  ])('explains a Warden %i', async (status, text) => {
    fetchMock(() => ({ status, body: { reason: status === 403 ? 'forbidden' : 'temporarily_unavailable' } }))
    const w = mount(WardenSecretPicker, { global: withAbility(MANAGE) })
    await flushPromises()
    expect(w.find('[data-test=warden-error]').text()).toContain(text)
    w.unmount()
  })

  it('lists name, username and folder only and emits the selection', async () => {
    fetchMock(() => ({ body: secrets }))
    const w = mount(WardenSecretPicker, { global: withAbility(MANAGE) })
    await flushPromises()
    const item = w.find('[data-test=warden-secret]')
    expect(item.text()).toContain('zax-5 IPMI')
    expect(item.text()).toContain('ADMIN')
    expect(item.text()).toContain('/infra/bmc')
    await item.trigger('click')
    expect(w.emitted('select')?.[0]?.[0]).toMatchObject({ id: REF, name: 'zax-5 IPMI' })
    await w.find('[data-test=warden-cancel]').trigger('click')
    expect(w.emitted('cancel')).toBeTruthy()
    w.unmount()
  })

  it('says when nothing matches', async () => {
    fetchMock(() => ({ body: { items: [] } }))
    const w = mount(WardenSecretPicker, { global: withAbility(MANAGE) })
    await flushPromises()
    expect(w.find('[data-test=warden-empty]').exists()).toBe(true)
    w.unmount()
  })
})

describe('Power / KVM tab (024 US3)', () => {
  const POWER = [{ action: 'control', subject: 'Power' }, { action: 'access', subject: 'Kvm' }]
  const mountTab = async (rules: { action: string; subject: string }[]) => {
    const { default: IpmiKvm } = await import('@/views/devices/ipmi-kvm.vue')
    const w = mount(IpmiKvm, { props: { deviceId: 'd1' }, global: withAbility(rules) })
    await flushPromises()
    return w
  }

  it.each([
    [{ ...none }, 'No BMC credentials configured'],
    [{ ...ready, address: undefined, address_source: undefined, ready: false, reason: 'bmc_no_address' }, 'No BMC address'],
    [{ ...ready, access: 'forbidden', secret: undefined, ready: false, reason: 'bmc_secret_forbidden' }, 'no access'],
    [{ ...ready, access: 'not_found', secret: undefined, ready: false, reason: 'bmc_secret_not_found' }, 'no longer exists'],
    [{ ...ready, access: 'unavailable', secret: undefined, ready: false, reason: 'warden_unavailable' }, 'Warden is unavailable'],
  ] as [BmcStatus, string][])('explains a status that is not ready and calls no BMC route (%#)', async (st, text) => {
    const calls = fetchMock(() => ({ body: st }))
    const w = await mountTab(POWER)
    expect(w.find('[data-test=oob-blocked]').text()).toContain(text)
    expect(calls.some((c) => /\/(power|sensors|sel)$/.test(c.url))).toBe(false)
    expect(w.find('[data-test=power-on]').exists()).toBe(false)
    expect(w.find('[data-test=kvm-start]').exists()).toBe(false)
    w.unmount()
  })

  it('offers attaching credentials only to device managers', async () => {
    fetchMock(() => ({ body: none }))
    let w = await mountTab(POWER)
    expect(w.find('[data-test=oob-attach-hint]').exists()).toBe(false)
    w.unmount()
    fetchMock(() => ({ body: none }))
    w = await mountTab([...POWER, { action: 'configure', subject: 'DeviceBmc' }])
    expect(w.find('[data-test=oob-attach-hint]').text()).toContain('BMC credentials card')
    w.unmount()
  })

  it('loads power and sensors when ready and shows the BMC answer reasons', async () => {
    const calls = fetchMock((url) => {
      if (url.endsWith('/bmc')) return { body: ready }
      if (url.endsWith('/power')) return { body: { on: true } }
      return { status: 504, body: { reason: 'bmc_unreachable', detail: { address: '10.1.112.14' } } }
    })
    const w = await mountTab(POWER)
    expect(calls.some((c) => c.url.endsWith('/power'))).toBe(true)
    expect(w.find('[data-test=oob-error]').text()).toContain('BMC 10.1.112.14 did not answer')
    expect(w.find('[data-test=power-cycle]').exists()).toBe(true)
    expect(w.find('[data-test=kvm-start]').exists()).toBe(true)
    w.unmount()
  })

  it.each([
    ['bmc_auth_failed', 502, 'rejected the credentials'],
    ['bmc_error', 502, 'reported an error'],
    ['bmc_secret_forbidden', 403, 'no access'],
  ])('explains %s from a power call', async (reason, status, text) => {
    fetchMock((url) => (url.endsWith('/bmc') ? { body: ready } : { status, body: { reason, detail: { address: '10.1.112.14' } } }))
    const w = await mountTab(POWER)
    expect(w.find('[data-test=oob-error]').text()).toContain(text)
    w.unmount()
  })

  // Feature 025: consoles live on the gateway's console origin (port 8444).
  const startKvm = async (consoleUrl: string) => {
    fetchMock((url) => {
      if (url.endsWith('/bmc')) return { body: ready }
      if (url.endsWith('/kvm-session')) return { status: 201, body: { token: 't', console_url: consoleUrl } }
      return { body: {} }
    })
    const w = await mountTab([{ action: 'access', subject: 'Kvm' }])
    await w.find('[data-test=kvm-start]').trigger('click')
    await flushPromises()
    return w
  }

  it.each([
    ['bmc_2fa_required', 409, 'BMC 10.1.112.14 requires two-factor login — the console cannot sign in; open its web UI directly.'],
    ['bmc_session_limit', 502, 'BMC 10.1.112.14 session limit reached — close other BMC web sessions and try again.'],
    ['bmc_auth_failed', 502, 'rejected the credentials'],
  ])('explains a refused KVM console login (%s)', async (reason, status, text) => {
    fetchMock((url) => {
      if (url.endsWith('/bmc')) return { body: ready }
      if (url.endsWith('/kvm-session')) return { status, body: { reason, detail: { address: '10.1.112.14' } } }
      return { body: {} }
    })
    const w = await mountTab([{ action: 'access', subject: 'Kvm' }])
    await w.find('[data-test=kvm-start]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test=oob-error]').text()).toContain(text)
    expect(w.find('iframe').exists()).toBe(false)
    w.unmount()
  })

  it('embeds a console served from the console origin', async () => {
    const url = 'https://portal.example.org:8444/bmc/d1/cgi/url_redirect.cgi?url_name=man_ikvm_html5_bootstrap&kvmtoken=t'
    const w = await startKvm(url)
    const frame = w.find('iframe')
    expect(frame.attributes('src')).toBe(url)
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(w.find('[data-test=kvm-no-origin]').exists()).toBe(false)
    w.unmount()
  })

  it.each(['/bmc/d1/?kvmtoken=t', 'http://portal.example.org:8444/bmc/d1/', 'javascript:alert(1)', 'not a url'])(
    'explains a console URL that is not on an https console origin (%s)',
    async (consoleUrl) => {
      const w = await startKvm(consoleUrl)
      expect(w.find('iframe').exists()).toBe(false)
      expect(w.find('[data-test=kvm-no-origin]').text()).toContain('KVM console origin not configured')
      w.unmount()
    },
  )

  it('hides power actions and KVM without the platform abilities', async () => {
    const calls = fetchMock(() => ({ body: ready }))
    const w = await mountTab([{ action: 'access', subject: 'Kvm' }])
    expect(w.find('[data-test=power-on]').exists()).toBe(false)
    expect(w.find('[data-test=kvm-start]').exists()).toBe(true)
    expect(calls.some((c) => c.url.endsWith('/power'))).toBe(false)
    w.unmount()
  })
})
