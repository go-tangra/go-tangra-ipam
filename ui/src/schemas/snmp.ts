import { z } from 'zod'
import type { SubnetSNMPInput } from '@/api/types'

// Feature 021: write-only SNMP credentials on a subnet. The form edits every
// field as text; the output is the exact request body of the chosen kind.

export const SNMP_MAX = 256
export const SNMP_MIN_PASSWORD = 8

export const SNMP_LEVELS = ['authNoPriv', 'authPriv'] as const
// Strongest first; MD5, SHA-1 and DES stay for old devices but are weak.
export const SNMP_AUTH_PROTOCOLS = ['SHA512', 'SHA384', 'SHA256', 'SHA224', 'SHA', 'MD5'] as const
export const SNMP_PRIV_PROTOCOLS = ['AES256', 'AES192', 'AES', 'DES'] as const
export const SNMP_WEAK = new Set<string>(['MD5', 'SHA', 'DES'])
const PROTOCOL_NAMES: Record<string, string> = {
  SHA512: 'SHA-512', SHA384: 'SHA-384', SHA256: 'SHA-256', SHA224: 'SHA-224', SHA: 'SHA-1', MD5: 'MD5',
  AES256: 'AES-256', AES192: 'AES-192', AES: 'AES-128', DES: 'DES',
}
/** Human label of a protocol, flagged when weak. */
export const protocolLabel = (p: string) => (PROTOCOL_NAMES[p] ?? p) + (SNMP_WEAK.has(p) ? ' (weak)' : '')

const chars = (s: string | undefined) => [...(s ?? '')].length

/** PUT /subnets/{id}/snmp. */
export const snmpSchema = z
  .object({
    version: z.enum(['2', '3']),
    community: z.string().optional(),
    user: z.string().optional(),
    security_level: z.string().optional(),
    auth_protocol: z.string().optional(),
    auth_password: z.string().optional(),
    priv_protocol: z.string().optional(),
    priv_password: z.string().optional(),
  })
  .superRefine((v, ctx) => {
    const need = (path: keyof typeof v, min = 1) => {
      const n = chars(v[path])
      if (n === 0) ctx.addIssue({ code: 'custom', path: [path], message: 'required' })
      else if (n < min) ctx.addIssue({ code: 'custom', path: [path], message: `at least ${min} characters` })
      else if (n > SNMP_MAX) ctx.addIssue({ code: 'custom', path: [path], message: `at most ${SNMP_MAX} characters` })
    }
    if (v.version === '2') {
      need('community')
      return
    }
    need('user')
    if (!(SNMP_LEVELS as readonly string[]).includes(v.security_level ?? '')) ctx.addIssue({ code: 'custom', path: ['security_level'], message: 'required' })
    if (!(SNMP_AUTH_PROTOCOLS as readonly string[]).includes(v.auth_protocol ?? '')) ctx.addIssue({ code: 'custom', path: ['auth_protocol'], message: 'required' })
    need('auth_password', SNMP_MIN_PASSWORD)
    if (v.security_level === 'authPriv') {
      if (!(SNMP_PRIV_PROTOCOLS as readonly string[]).includes(v.priv_protocol ?? '')) ctx.addIssue({ code: 'custom', path: ['priv_protocol'], message: 'required' })
      need('priv_password', SNMP_MIN_PASSWORD)
    }
  })
  .transform((v): SubnetSNMPInput => {
    if (v.version === '2') return { version: 2, community: v.community ?? '' }
    const level = v.security_level as 'authNoPriv' | 'authPriv'
    const out: SubnetSNMPInput = { version: 3, user: v.user ?? '', security_level: level, auth_protocol: v.auth_protocol ?? '', auth_password: v.auth_password ?? '' }
    if (level === 'authPriv') Object.assign(out, { priv_protocol: v.priv_protocol ?? '', priv_password: v.priv_password ?? '' })
    return out
  })
export type SnmpFormInput = z.input<typeof snmpSchema>

/** POST /subnets/{id}/snmp/test: one address of the subnet. */
export const snmpTestSchema = z.object({ address: z.string().trim().pipe(z.union([z.ipv4(), z.ipv6()], { error: 'an IPv4 or IPv6 address' })) })
