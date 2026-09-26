import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { DeviceHostSync, HostSyncSettings, HostSyncStatus, HypervisorGuest, IPAddress, ResyncResult } from '@/api/types'

// The host sync (feature 020): tenant settings and status, per-device report
// provenance, re-sync and hypervisor guests.
export const useHostSync = defineStore('ipam-hostsync', () => {
  const settings = ref<HostSyncSettings | null>(null)
  const status = ref<HostSyncStatus | null>(null)
  const loading = ref(false)
  const error = ref('')

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      ;[settings.value, status.value] = await Promise.all([
        api<HostSyncSettings>('GET', 'host-sync/settings'),
        api<HostSyncStatus>('GET', 'host-sync/status'),
      ])
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function save(body: HostSyncSettings): Promise<HostSyncSettings> {
    const s = await api<HostSyncSettings>('PUT', 'host-sync/settings', {
      enabled: body.enabled, full_interval_minutes: body.full_interval_minutes, excluded_interfaces: body.excluded_interfaces,
    })
    settings.value = s
    status.value = await api<HostSyncStatus>('GET', 'host-sync/status')
    return s
  }

  async function resyncAll(): Promise<void> {
    await api<{ scheduled: boolean }>('POST', 'host-sync/resync', {})
  }

  async function device(id: string): Promise<DeviceHostSync> {
    return api<DeviceHostSync>('GET', 'devices/' + id + '/host-sync')
  }

  async function resyncDevice(id: string): Promise<ResyncResult> {
    return api<ResyncResult>('POST', 'devices/' + id + '/host-sync', {})
  }

  async function guests(id: string): Promise<HypervisorGuest[]> {
    const res = await api<{ items: HypervisorGuest[] | null }>('GET', 'devices/' + id + '/guests')
    return res.items ?? []
  }

  async function clearConflict(addressId: string): Promise<IPAddress> {
    return api<IPAddress>('POST', 'ip-addresses/' + addressId + '/clear-conflict', {})
  }

  return { settings, status, loading, error, load, save, resyncAll, device, resyncDevice, guests, clearConflict }
})
