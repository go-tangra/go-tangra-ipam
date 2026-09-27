import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { ARPSettings } from '@/api/types'

// Per-tenant ARP collection settings (feature 022).
export const useArp = defineStore('ipam-arp', () => {
  const settings = ref<ARPSettings | null>(null)
  const error = ref('')

  async function load(): Promise<void> {
    error.value = ''
    try {
      settings.value = await api<ARPSettings>('GET', 'arp/settings')
    } catch (e) {
      error.value = (e as Error).message
    }
  }

  async function save(body: Pick<ARPSettings, 'enabled' | 'proxy_threshold' | 'excluded_devices'>): Promise<ARPSettings> {
    settings.value = await api<ARPSettings>('PUT', 'arp/settings', {
      enabled: body.enabled, proxy_threshold: body.proxy_threshold, excluded_devices: body.excluded_devices,
    })
    return settings.value
  }

  return { settings, error, load, save }
})
