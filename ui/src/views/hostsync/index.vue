<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAbility } from '@casl/vue'
import { UiPage, UiAlert, UiCard, UiButton, UiStatusChip, UiKeyValueTable, UiForm, UiSwitch, UiNumberInput, UiTextarea, type KeyValue } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { useHostSync } from '@/stores/hostsync'
import { hostSyncSettingsSchema } from '@/schemas'
import { describe } from '@/api/client'
import ArpSettingsCard from '@/components/ArpSettingsCard.vue'

const store = useHostSync()
const ability = useAbility()
// Settings are a tenant-wide decision (hostsync:manage); re-sync follows devices:manage.
const canManage = computed(() => ability.can('manage', 'HostSync'))
const canResync = computed(() => ability.can('resync', 'HostSync'))
const stateColors = { ok: 'success', degraded: 'warning', disabled: 'neutral' } as const
const message = ref('')
const actionError = ref('')

const form = useZodForm(hostSyncSettingsSchema, {
  onSubmit: (v) => store.save({ enabled: v.enabled, full_interval_minutes: v.full_interval_minutes, excluded_interfaces: v.excluded_interfaces }),
  onSuccess: () => (message.value = 'Settings saved'),
})
function resetForm(): void {
  const s = store.settings
  if (s) form.reset({ enabled: s.enabled, full_interval_minutes: s.full_interval_minutes, excluded_interfaces: (s.excluded_interfaces ?? []).join('\n') })
}
async function load(): Promise<void> {
  await store.load()
  resetForm()
}
onMounted(load)

const when = (t?: string) => (t ? new Date(t).toLocaleString() : '—')
const status = computed<KeyValue[]>(() => {
  const s = store.status
  if (!s) return []
  return [
    { label: 'Automatic sync', value: s.enabled ? 'enabled' : 'disabled' }, { label: 'Last error', value: s.last_error || '—' },
    { label: 'Last poll', value: when(s.last_poll_at) }, { label: 'Last full reconcile', value: when(s.last_reconcile_at) },
    { label: 'Next full reconcile', value: when(s.next_reconcile_at) }, { label: 'Hosts reported', value: String(s.hosts_reported ?? 0) },
    { label: 'Hosts failed', value: String(s.hosts_failed ?? 0) }, { label: 'Devices no longer reported', value: String(s.devices_not_reported ?? 0) },
    { label: 'Addresses in conflict', value: String(s.addresses_in_conflict ?? 0) },
  ]
})
const resyncing = ref(false)
async function resyncAll(): Promise<void> {
  resyncing.value = true
  actionError.value = ''
  message.value = ''
  try {
    await store.resyncAll()
    message.value = 'A full re-sync of every host is scheduled'
  } catch (e) {
    actionError.value = describe(e)
  } finally {
    resyncing.value = false
  }
}
</script>

<template>
  <UiPage title="Host sync">
    <template #badges><UiStatusChip v-if="store.status" :status="store.status.state" :colors="stateColors" data-test="hostsync-state" /></template>
    <template #actions>
      <UiButton v-if="canResync" size="sm" variant="soft" icon="mdi-sync" :loading="resyncing" data-test="hostsync-resync-all" @click="resyncAll">Re-sync all hosts</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="load" />
    </template>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiAlert v-if="actionError" kind="error" class="mb-3">{{ actionError }}</UiAlert>
    <UiAlert v-if="message" kind="success" class="mb-3">{{ message }}</UiAlert>
    <UiCard class="mb-4" data-test="hostsync-status"><UiKeyValueTable :items="status" :columns="2" /></UiCard>
    <UiCard v-if="store.settings" data-test="hostsync-settings">
      <p class="mb-3 text-sm text-base-content/70">
        Hosts running the inventory agent keep their devices, interfaces and addresses current in IPAM. Every change is audited.
        Interfaces whose name matches an exclusion pattern (and loopback interfaces) are never recorded.
      </p>
      <UiForm :form="form">
        <div class="flex flex-col gap-3">
          <UiSwitch v-bind="form.field('enabled')" label="Apply host reports automatically" :disabled="!canManage" />
          <UiNumberInput v-bind="form.field('full_interval_minutes')" label="Full reconcile every (minutes)" :min="15" :max="1440" :disabled="!canManage" required />
          <UiTextarea v-bind="form.field('excluded_interfaces')" label="Excluded interfaces (one pattern per line)" :rows="6" :disabled="!canManage" />
        </div>
      </UiForm>
      <div v-if="canManage" class="mt-3 flex justify-end gap-2">
        <UiButton variant="text" @click="resetForm">Reset</UiButton>
        <UiButton :loading="form.submitting.value" data-test="hostsync-save" @click="form.submit()">Save</UiButton>
      </div>
      <p v-else class="mt-3 text-sm text-base-content/70" data-test="hostsync-readonly">Only IPAM administrators can change these settings.</p>
    </UiCard>
    <ArpSettingsCard class="mt-4" />
  </UiPage>
</template>
