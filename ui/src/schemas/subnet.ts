import { z } from 'zod'
import { cidr, nonEmpty, optionalString } from '@go-tangra/ui/forms'

export const SUBNET_STATUSES = ['active', 'reserved', 'deprecated', 'deleted'] as const

/** POST/PUT /subnets. A child names its parent; the server checks containment. */
export const subnetSchema = z.object({
  name: nonEmpty(200),
  cidr,
  parent_id: optionalString(64),
  description: optionalString(2000),
  status: z.enum(SUBNET_STATUSES).optional(),
  gateway: optionalString(45).pipe(z.union([z.ipv4(), z.ipv6()]).optional()),
  dns_servers: optionalString(500),
  vlan_id: optionalString(64),
  location_id: optionalString(64),
})
export type SubnetInput = z.output<typeof subnetSchema>

export const subnetFilterSchema = z.object({
  q: z.string().trim().max(200).optional(),
  status: z.enum(SUBNET_STATUSES).optional(),
  ip_version: z.enum(['4', '6']).optional(),
})

/** POST /subnets/{id}/split: the child prefix length to carve the parent into. */
export const splitSchema = z.object({
  prefix_length: z.coerce.number().int().min(1).max(128),
})
export type SplitInput = z.output<typeof splitSchema>
