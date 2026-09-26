import { z } from 'zod'

/** One interface exclusion pattern (Go path.Match; the server's charset). */
export const exclusionPattern = /^[A-Za-z0-9*?._:-]{1,64}$/

/**
 * PUT /host-sync/settings. The exclusions are edited as text (one pattern per
 * line or separated by commas/spaces) and posted as a list.
 */
export const hostSyncSettingsSchema = z.object({
  enabled: z.boolean(),
  full_interval_minutes: z.coerce.number().int().min(15, 'at least 15 minutes').max(1440, 'at most 1440 minutes (24 h)'),
  excluded_interfaces: z
    .string()
    .max(64 * 65)
    .transform((s) => s.split(/[\s,]+/).filter(Boolean))
    .pipe(z.array(z.string().regex(exclusionPattern, 'patterns use letters, digits and * ? . _ : - (max 64)')).max(64, 'at most 64 patterns')),
})
export type HostSyncSettingsInput = z.input<typeof hostSyncSettingsSchema>
