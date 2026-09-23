import { z } from 'zod'
import { ipv4, nonEmpty, optionalString, positiveInt } from '@freya/ui/forms'

export const DEVICE_TYPES = ['server', 'vm', 'router', 'switch', 'firewall', 'load_balancer', 'access_point', 'storage', 'printer', 'phone', 'workstation', 'container', 'other'] as const
export const DEVICE_STATUSES = ['active', 'planned', 'staged', 'decommissioned', 'offline', 'failed', 'available'] as const

export const deviceSchema = z.object({
  name: nonEmpty(200),
  device_type: z.enum(DEVICE_TYPES),
  status: z.enum(DEVICE_STATUSES).optional(),
  manufacturer: optionalString(200),
  model: optionalString(200),
  management_ip: optionalString(45).pipe(ipv4.optional()),
  location_id: optionalString(64),
  // Rack placement: the bottom U it occupies and how many U it spans.
  rack_id: optionalString(64),
  rack_position: positiveInt.pipe(z.number().max(100)),
  device_height_u: positiveInt.pipe(z.number().max(50)),
})
export type DeviceInput = z.output<typeof deviceSchema>

export const deviceFilterSchema = z.object({
  q: z.string().trim().max(200).optional(),
  device_type: z.enum(DEVICE_TYPES).optional(),
  status: z.enum(DEVICE_STATUSES).optional(),
})

/** Placing a device in a rack: the bottom U it sits in and its height. */
export const rackPlacementSchema = z.object({
  device_id: nonEmpty(64),
  rack_position: z.coerce.number().int().min(1).max(100),
  device_height_u: z.coerce.number().int().min(1).max(50),
})
export type RackPlacementInput = z.output<typeof rackPlacementSchema>
