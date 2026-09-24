import { z } from 'zod'
import { cidr, ipv4, nonEmpty, optionalString, positiveInt } from '@go-tangra/ui/forms'

/** "Check IP membership" lookup. */
export const checkIpSchema = z.object({ ip: ipv4 })

export const GROUP_STATUSES = ['active', 'inactive'] as const
export const MEMBER_TYPES = ['address', 'range', 'subnet'] as const

/** POST/PUT /ip-groups and /host-groups share this payload. */
export const groupSchema = z.object({
  name: nonEmpty(200),
  description: optionalString(2000),
  status: z.enum(GROUP_STATUSES).optional(),
})
export type GroupInput = z.output<typeof groupSchema>
export const ipGroupSchema = groupSchema
export const hostGroupSchema = groupSchema

/** An IP-group member; the value grammar follows the member type. */
export const ipGroupMemberSchema = z
  .object({
    member_type: z.enum(MEMBER_TYPES),
    value: nonEmpty(100),
    description: optionalString(500),
    sequence: positiveInt.pipe(z.number().max(9999)),
  })
  .superRefine((m, ctx) => {
    const issue = (message: string) => ctx.addIssue({ code: 'custom', path: ['value'], message })
    if (m.member_type === 'address' && !ipv4.safeParse(m.value).success) issue('Enter an IPv4 address, e.g. 10.0.0.5.')
    if (m.member_type === 'subnet' && !cidr.safeParse(m.value).success) issue('Enter a CIDR block, e.g. 10.0.0.0/24.')
    if (m.member_type === 'range') {
      const [from, to] = m.value.split('-').map((s) => s.trim())
      if (!from || !to || !ipv4.safeParse(from).success || !ipv4.safeParse(to).success) issue('Enter a range as "10.0.0.10-10.0.0.20".')
    }
  })
export type IPGroupMemberInput = z.output<typeof ipGroupMemberSchema>

/** A host-group member is a device reference. */
export const hostGroupMemberSchema = z.object({
  device_id: nonEmpty(64),
  sequence: positiveInt.pipe(z.number().max(9999)),
})
export type HostGroupMemberInput = z.output<typeof hostGroupMemberSchema>
