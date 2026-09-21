import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { Stats } from '@/api/types'

export const useStats = defineStore('ipam-stats', () => {
  const snapshot = ref<Stats | null>(null)
  const loaded = ref(false)
  const error = ref('')

  // load fetches the tenant rollup; a failure is non-fatal (the dashboard
  // falls back to figures derived from the loaded entity lists).
  async function load(): Promise<void> {
    try {
      snapshot.value = await api<Stats>('GET', 'stats')
      loaded.value = true
    } catch (e) {
      error.value = (e as Error).message
    }
  }

  return { snapshot, loaded, error, load }
})
