import { z } from 'zod'

const deviceId = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/

/** PUT /arp/settings (feature 022): mirrors ARPSettingsInput. */
export const arpSettingsSchema = z.object({
  enabled: z.boolean(),
  proxy_threshold: z.coerce.number().int().min(2, 'at least 2').max(256, 'at most 256'),
  excluded_devices: z.array(z.string().regex(deviceId, 'not a device id')).max(256, 'at most 256 devices'),
})
export type ArpSettingsInput = z.input<typeof arpSettingsSchema>
