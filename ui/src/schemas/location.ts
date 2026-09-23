import { z } from 'zod'
import { nonEmpty, optionalString, positiveInt } from '@freya/ui/forms'

export const LOCATION_TYPES = ['region', 'country', 'city', 'datacenter', 'building', 'floor', 'room', 'rack', 'site', 'branch'] as const
export const LOCATION_STATUSES = ['active', 'planned', 'decommissioned'] as const

export const locationSchema = z
  .object({
    name: nonEmpty(200),
    location_type: z.enum(LOCATION_TYPES),
    code: optionalString(64),
    parent_id: optionalString(64),
    status: z.enum(LOCATION_STATUSES).optional(),
    rack_size_u: positiveInt.pipe(z.number().max(100)),
    description: optionalString(2000),
    address: optionalString(500),
    city: optionalString(200),
    country: optionalString(200),
  })
  .superRefine((l, ctx) => {
    if (l.location_type === 'rack' && l.rack_size_u < 1) ctx.addIssue({ code: 'custom', path: ['rack_size_u'], message: 'A rack needs a height in U (e.g. 42).' })
  })
export type LocationInput = z.output<typeof locationSchema>
