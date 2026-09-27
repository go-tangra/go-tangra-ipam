// BMC credentials (feature 024): the Warden secret picker calls Warden's own
// user API through the gateway with the shell session (Warden decides which
// secrets the user may read), and the reason texts of the BMC routes.
import { api, ApiError, describe } from './client'
import type { BmcReason, WardenSecretItem } from './types'

export const WARDEN_BASE = '/api/warden/v1'

// searchWardenSecrets returns the readable secrets matching q (the readable
// root secrets when q is empty). Only id, name, username and folder are kept;
// the picker never requests a password.
export async function searchWardenSecrets(q: string, signal?: AbortSignal): Promise<WardenSecretItem[]> {
  const term = q.trim().slice(0, 200)
  const opts = (query: Record<string, string | number | boolean>) => (signal ? { query, signal } : { query })
  const res = term
    ? await api<{ items: WardenSecretItem[] | null }>('GET', WARDEN_BASE + '/secrets/search', undefined, opts({ q: term, limit: 20 }))
    : await api<{ items: WardenSecretItem[] | null }>('GET', WARDEN_BASE + '/secrets', undefined, opts({ root: true, limit: 20 }))
  return (res.items ?? []).map(({ id, name, username, folder_path }) => ({
    id, name, ...(username ? { username } : {}), ...(folder_path ? { folder_path } : {}),
  }))
}

// wardenErrorText explains a failed Warden listing.
export function wardenErrorText(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.status === 401 || e.status === 403) return 'You cannot read Warden secrets (Warden permission secrets:read is required).'
    if (e.status === 404 || e.status >= 500) return 'Warden is unavailable — try again later.'
  }
  return describe(e)
}

const REASONS: Record<BmcReason, (address?: string) => string> = {
  bmc_not_configured: () => 'No BMC credentials configured — attach a Warden secret.',
  bmc_no_address: () => 'No BMC address — set the management IP or let the inventory agent report the BMC.',
  bmc_secret_forbidden: () => 'You have no access to the BMC credentials in Warden — ask the secret owner to share it.',
  bmc_secret_not_found: () => 'The Warden secret no longer exists — attach another one.',
  warden_unavailable: () => 'Warden is unavailable — try again later.',
  bmc_unreachable: (a) => `BMC ${a ?? ''} did not answer.`.replace('  ', ' '),
  bmc_auth_failed: (a) => `BMC ${a ?? ''} rejected the credentials — check the Warden secret.`.replace('  ', ' '),
  bmc_error: (a) => `BMC ${a ?? ''} reported an error.`.replace('  ', ' '),
}

export function isBmcReason(r: unknown): r is BmcReason {
  return typeof r === 'string' && r in REASONS
}

// bmcReasonText explains a reason (with the BMC address when known).
export function bmcReasonText(reason: BmcReason, address?: string): string {
  return REASONS[reason](address)
}

// bmcErrorText explains a failed BMC call: its reason when the API gave one,
// the generic kit wording otherwise.
export function bmcErrorText(e: unknown): string {
  if (e instanceof ApiError && isBmcReason(e.reason)) {
    const address = typeof e.detail?.address === 'string' ? e.detail.address : undefined
    return bmcReasonText(e.reason, address)
  }
  return describe(e)
}
