import { z } from 'zod'
import { nonEmpty, optionalString, positiveInt } from '@freya/ui/forms'

export const ADDRESS_STATUSES = ['active', 'reserved', 'dhcp', 'deprecated', 'offline'] as const
export const ADDRESS_TYPES = ['host', 'gateway', 'broadcast', 'network', 'virtual', 'anycast'] as const

/** RFC 1123 host name label(s). */
export const hostname = z.string().trim().max(253).regex(/^(?=.{1,253}$)([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i, 'Use a valid host name.')

const count = positiveInt.pipe(z.number().int().min(1, 'At least 1.').max(1024, 'At most 1024.')).meta({ kind: 'number' })

/** Next-free allocation (POST /ip-addresses/allocate). */
export const allocateSchema = z.object({
  subnet_id: nonEmpty(64),
  hostname: optionalString(253).pipe(hostname.optional()),
})
/** Bulk allocation (POST /ip-addresses/bulk-allocate). */
export const bulkAllocateSchema = z.object({
  subnet_id: nonEmpty(64),
  count,
  hostname_prefix: optionalString(60).pipe(z.string().regex(/^[a-z0-9-]+$/i, 'Letters, digits and dashes only.').optional()),
})
/** Free-address suggestion (GET /ip-addresses/suggest). */
export const suggestSchema = z.object({
  subnet_id: nonEmpty(64),
  count,
})

export const addressFilterSchema = z.object({
  subnet_id: z.string().optional(),
  status: z.enum(ADDRESS_STATUSES).optional(),
  address_type: z.enum(ADDRESS_TYPES).optional(),
  hostname: z.string().trim().max(253).optional(),
})
