/**
 * Helper for referencing a Warden secret from a device (the IPMI/BMC
 * credentials). It calls IPAM's own proxy endpoints — IPAM forwards to the
 * Warden module over the mTLS mesh with the user's context — so the frontend
 * only ever talks to IPAM. Only secret *metadata* is returned, never the value.
 */
import { ipamApi, type RequestOptions } from './client';

export interface WardenSecretRef {
  id?: string;
  name?: string;
  folderPath?: string;
  username?: string;
}

interface SearchWardenSecretsResponse {
  secrets?: WardenSecretRef[];
}

interface GetWardenSecretResponse {
  secret?: WardenSecretRef;
}

export const WardenService = {
  /** Search secrets by name (metadata only). */
  searchSecrets: async (
    query?: string,
    options?: RequestOptions,
  ): Promise<WardenSecretRef[]> => {
    const qs = new URLSearchParams({ limit: '25' });
    if (query) qs.set('query', query);
    const resp = await ipamApi.get<SearchWardenSecretsResponse>(
      `/warden-secrets?${qs.toString()}`,
      options,
    );
    return resp.secrets ?? [];
  },

  /** Resolve a single secret's metadata by id (for display). */
  getSecret: async (
    id: string,
    options?: RequestOptions,
  ): Promise<WardenSecretRef | null> => {
    const resp = await ipamApi.get<GetWardenSecretResponse>(
      `/warden-secrets/${encodeURIComponent(id)}`,
      options,
    );
    return resp.secret ?? null;
  },
};
