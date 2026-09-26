import { z } from 'zod'
import type { SubnetSNMPInput } from '@/api/types'

// Feature 021: write-only SNMP credentials on a subnet. The form edits every
// field as text; the output is the exact request body of the chosen kind.

export const SNMP_MAX = 256

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
    if (v.version === '2') need('community')
  })
  .transform((v): SubnetSNMPInput => ({ version: 2, community: v.community ?? '' }))
export type SnmpFormInput = z.input<typeof snmpSchema>
