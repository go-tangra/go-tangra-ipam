import { describe, expect, it } from 'vitest'
import { allocateSchema, bulkAllocateSchema, checkIpSchema, deviceSchema, hostname, locationSchema, startScanSchema, subnetSchema, subnetFilterSchema, vlanSchema } from '@/schemas'

// T033: IPAM write schemas mirror api/openapi/ipam.yaml payloads; the CIDR and
// address grammars come from the kit and refuse malformed input before a request.
describe('ipam schemas', () => {
  it('subnet: CIDR v4/v6 accepted, malformed refused, blanks dropped', () => {
    for (const ok of ['10.0.0.0/8', ' 192.168.1.0/24 ', '2001:db8::/32', 'fd00::/8']) expect(subnetSchema.safeParse({ name: 'n', cidr: ok }).success, ok).toBe(true)
    for (const bad of ['999.1.1.1/8', '10.0.0.0/33', '10.0.0.0', '10.0.0/24', '2001:db8::/129', 'abc', '']) expect(subnetSchema.safeParse({ name: 'n', cidr: bad }).success, bad).toBe(false)
    expect(subnetSchema.parse({ name: ' Core ', cidr: '10.0.0.0/8', description: '', vlan_id: '', status: 'reserved' })).toEqual({ name: 'Core', cidr: '10.0.0.0/8', description: undefined, vlan_id: undefined, location_id: undefined, status: 'reserved' })
    expect(subnetSchema.safeParse({ name: 'n', cidr: '10.0.0.0/8', status: 'weird' }).success).toBe(false)
    expect(subnetFilterSchema.safeParse({ ip_version: '5' }).success).toBe(false)
  })
  it('CIDR fuzz: random octets/prefixes only pass when every part is in range', () => {
    let seed = 42
    const rnd = (n: number) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n }
    for (let i = 0; i < 300; i++) {
      const octets = [rnd(300), rnd(300), rnd(300), rnd(300)]
      const prefix = rnd(40)
      const s = `${octets.join('.')}/${prefix}`
      const valid = octets.every((o) => o <= 255) && prefix <= 32
      expect(subnetSchema.safeParse({ name: 'n', cidr: s }).success, s).toBe(valid)
    }
  })
  it('addresses: host names per RFC 1123, bulk count 1–1024, prefix letters/digits/dashes', () => {
    for (const ok of ['web-01', 'web-01.example.org', 'a', 'x'.repeat(63)]) expect(hostname.safeParse(ok).success, ok).toBe(true)
    for (const bad of ['-web', 'web-', 'we b', 'x'.repeat(64), 'a..b', '']) expect(hostname.safeParse(bad).success, bad).toBe(false)
    expect(allocateSchema.parse({ subnet_id: 's1', hostname: '' })).toEqual({ subnet_id: 's1', hostname: undefined })
    expect(allocateSchema.safeParse({ subnet_id: '', hostname: 'ok' }).success).toBe(false)
    expect(bulkAllocateSchema.parse({ subnet_id: 's1', count: '4', hostname_prefix: 'node' })).toEqual({ subnet_id: 's1', count: 4, hostname_prefix: 'node' })
    expect(bulkAllocateSchema.safeParse({ subnet_id: 's1', count: 0 }).success).toBe(false)
    expect(bulkAllocateSchema.safeParse({ subnet_id: 's1', count: 1025 }).success).toBe(false)
    expect(bulkAllocateSchema.safeParse({ subnet_id: 's1', count: 2, hostname_prefix: 'no spaces' }).success).toBe(false)
  })
  it('VLAN id 1–4094 coerced to a number; device/location enums enforced', () => {
    expect(vlanSchema.parse({ vlan_id: '100', name: 'Users' })).toMatchObject({ vlan_id: 100 })
    for (const bad of [0, 4095, 'abc', 1.5]) expect(vlanSchema.safeParse({ vlan_id: bad, name: 'x' }).success, String(bad)).toBe(false)
    expect(deviceSchema.safeParse({ name: 'sw1', device_type: 'toaster' }).success).toBe(false)
    expect(locationSchema.safeParse({ name: 'DC1', location_type: 'moon' }).success).toBe(false)
  })
  it('scan + membership check: subnet required, IPv4 only for the lookup', () => {
    expect(startScanSchema.parse({ subnet_id: 's1', enable_snmp: true })).toEqual({ subnet_id: 's1', enable_snmp: true, enable_dns_update: undefined })
    expect(startScanSchema.safeParse({ subnet_id: '' }).success).toBe(false)
    expect(checkIpSchema.safeParse({ ip: ' 10.1.2.3 ' }).success).toBe(true)
    for (const bad of ['10.1.2', '256.1.1.1', '2001:db8::1', 'x']) expect(checkIpSchema.safeParse({ ip: bad }).success, bad).toBe(false)
  })
})
