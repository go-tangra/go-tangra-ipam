import { z } from 'zod'
import { nonEmpty } from '@freya/ui/forms'

/** POST /ip-scans payload. */
export const startScanSchema = z.object({
  subnet_id: nonEmpty(64),
  enable_snmp: z.boolean().optional(),
  enable_dns_update: z.boolean().optional(),
})
export type StartScanInput = z.output<typeof startScanSchema>
