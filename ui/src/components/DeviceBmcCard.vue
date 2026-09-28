<script setup lang="ts">
// BMC credentials of one device (feature 024): a reference to a Warden
// secret. The status is read for the viewing user (Warden metadata only);
// device managers attach a secret they can read, change or clear it.
import { computed, ref, watch } from 'vue'
import { useAbility } from '@casl/vue'
import { UiAlert, UiBadge, UiButton, UiCard, useConfirm } from '@go-tangra/ui'
import { useDevices } from '@/stores/devices'
import { bmcErrorText, bmcReasonText } from '@/api/bmc'
import { describe } from '@/api/client'
import type { BmcStatus, WardenSecretItem } from '@/api/types'
import WardenSecretPicker from './WardenSecretPicker.vue'

const props = defineProps<{ deviceId: string }>()
const emit = defineEmits<{ (e: 'status', s: BmcStatus): void; (e: 'changed'): void }>()

const store = useDevices()
const ability = useAbility()
const confirm = useConfirm()
const canManage = computed(() => ability.can('configure', 'DeviceBmc'))

const status = ref<BmcStatus | null>(null)
const error = ref('')
const picking = ref(false)
const saving = ref(false)

function show(s: BmcStatus): void {
  status.value = s
  emit('status', s)
}
async function load(): Promise<void> {
  error.value = ''
  try {
    show(await store.bmc(props.deviceId))
  } catch (e) {
    error.value = describe(e)
  }
}
watch(() => props.deviceId, load, { immediate: true })

async function pick(item: WardenSecretItem): Promise<void> {
  saving.value = true
  error.value = ''
  try {
    show(await store.setBmc(props.deviceId, item.id))
    picking.value = false
    emit('changed')
  } catch (e) {
    error.value = bmcErrorText(e)
  } finally {
    saving.value = false
  }
}
async function clear(): Promise<void> {
  const name = status.value?.secret?.name ?? 'the Warden secret'
  if (!(await confirm.ask({ title: 'Clear BMC credentials?', text: `The device no longer uses ${name}. Power, sensors and KVM stop until another secret is attached.`, danger: true, confirmLabel: 'Clear' }))) return
  error.value = ''
  try {
    await store.clearBmc(props.deviceId)
    emit('changed')
    await load()
  } catch (e) {
    error.value = bmcErrorText(e)
  }
}

const statusText = computed(() => {
  const s = status.value
  if (!s) return ''
  if (!s.configured) return bmcReasonText('bmc_not_configured')
  switch (s.access) {
    case 'ok':
      return [s.secret?.name, s.secret?.username, s.secret?.folder_path].filter(Boolean).join(' · ')
    case 'forbidden':
      return 'Configured — you have no access to this Warden secret.'
    case 'not_found':
      return bmcReasonText('bmc_secret_not_found')
    default:
      return bmcReasonText('warden_unavailable')
  }
})
const stateColor = computed(() => {
  const s = status.value
  if (!s?.configured) return 'neutral'
  return s.access === 'ok' ? 'success' : s.access === 'unavailable' ? 'warning' : 'error'
})
const addressText = computed(() => {
  const s = status.value
  if (!s) return ''
  if (!s.address) return bmcReasonText('bmc_no_address')
  return `BMC address ${s.address} (${s.address_source === 'reported' ? 'reported by the agent' : 'management IP'})`
})
</script>

<template>
  <UiCard title="BMC credentials" data-test="bmc-card">
    <UiAlert v-if="error" kind="error" class="mb-3" data-test="bmc-error">{{ error }}</UiAlert>
    <div v-if="status" class="flex flex-wrap items-center gap-2 text-sm" data-test="bmc-status">
      <UiBadge :color="stateColor" soft size="sm">{{ status.configured ? 'Warden' : 'none' }}</UiBadge>
      <span>{{ statusText }}</span>
    </div>
    <p v-if="status" class="mt-2 text-sm text-base-content/70" data-test="bmc-address">{{ addressText }}</p>
    <p class="mt-2 text-xs text-base-content/70">The password stays in Warden; it is fetched for you at each power, sensor or KVM action.</p>
    <template v-if="canManage">
      <div v-if="!picking" class="mt-3 flex flex-wrap gap-2">
        <UiButton size="sm" variant="soft" icon="mdi-key-link" data-test="bmc-attach" @click="picking = true">{{ status?.configured ? 'Change' : 'Attach Warden secret' }}</UiButton>
        <UiButton v-if="status?.configured" size="sm" variant="text" color="error" icon="mdi-key-remove" data-test="bmc-clear" @click="clear">Clear</UiButton>
      </div>
      <div v-else class="mt-3" :aria-busy="saving">
        <WardenSecretPicker @select="pick" @cancel="picking = false" />
      </div>
    </template>
    <p v-else class="mt-3 text-sm text-base-content/70" data-test="bmc-readonly">Only users who manage devices can change the BMC credentials.</p>
  </UiCard>
</template>
