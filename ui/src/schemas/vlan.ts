import { z } from 'zod'
import { nonEmpty, optionalString } from '@freya/ui/forms'

export const VLAN_STATUSES = ['active', 'reserved', 'deprecated'] as const

export const vlanSchema = z.object({
  vlan_id: z.coerce.number().int().min(1).max(4094),
  name: nonEmpty(200),
  domain: optionalString(200),
  status: z.enum(VLAN_STATUSES).optional(),
})
export type VlanInput = z.output<typeof vlanSchema>

export const vlanFilterSchema = z.object({
  domain: z.string().trim().max(200).optional(),
  status: z.enum(VLAN_STATUSES).optional(),
})
