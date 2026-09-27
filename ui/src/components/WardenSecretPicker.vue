<script setup lang="ts">
// Picks a Warden secret for a device's BMC credentials (feature 024). The
// list comes from Warden's own API with the shell session, so it holds only
// secrets the signed-in user may read; only name, username and folder are
// shown and no password is ever requested.
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { UiAlert, UiButton, UiInput } from '@go-tangra/ui'
import { searchWardenSecrets, wardenErrorText } from '@/api/bmc'
import type { WardenSecretItem } from '@/api/types'

const emit = defineEmits<{ (e: 'select', item: WardenSecretItem): void; (e: 'cancel'): void }>()

const query = ref('')
const items = ref<WardenSecretItem[]>([])
const error = ref('')
const loading = ref(false)
let abort: AbortController | null = null

async function search(): Promise<void> {
  abort?.abort()
  const ctl = new AbortController()
  abort = ctl
  loading.value = true
  error.value = ''
  try {
    items.value = await searchWardenSecrets(query.value, ctl.signal)
  } catch (e) {
    if (ctl.signal.aborted) return
    items.value = []
    error.value = wardenErrorText(e)
  } finally {
    if (abort === ctl) loading.value = false
  }
}
onMounted(search)
onBeforeUnmount(() => abort?.abort())
</script>

<template>
  <div class="flex flex-col gap-3" data-test="warden-picker">
    <form class="flex items-end gap-2" role="search" @submit.prevent="search">
      <UiInput id="warden-secret-query" v-model="query" class="grow" label="Search Warden secrets" type="search" placeholder="Name, username or host" data-test="warden-query" />
      <UiButton type="submit" size="sm" variant="soft" icon="mdi-magnify" :loading="loading" data-test="warden-search" @click.prevent="search">Search</UiButton>
    </form>
    <UiAlert v-if="error" kind="error" data-test="warden-error">{{ error }}</UiAlert>
    <ul v-else-if="items.length" class="flex max-h-72 flex-col gap-1 overflow-y-auto" aria-label="Warden secrets">
      <li v-for="s in items" :key="s.id">
        <button type="button" class="flex w-full flex-col items-start rounded-box px-3 py-2 text-left hover:bg-base-200 focus-visible:bg-base-200" data-test="warden-secret" @click="emit('select', s)">
          <span class="font-medium">{{ s.name }}</span>
          <span class="text-xs text-base-content/70">{{ [s.username, s.folder_path].filter(Boolean).join(' · ') || '—' }}</span>
        </button>
      </li>
    </ul>
    <p v-else-if="!loading" class="text-sm text-base-content/70" data-test="warden-empty">No readable Warden secret matches. Search by name, or ask the owner to share the BMC secret with you.</p>
    <div class="flex justify-end">
      <UiButton variant="text" data-test="warden-cancel" @click="emit('cancel')">Cancel</UiButton>
    </div>
  </div>
</template>
