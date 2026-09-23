<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAbility } from '@casl/vue'
import { UiAlert, UiCard, UiButton, UiStatusChip, UiDataTable, useConfirm, type Column } from '@freya/ui'
import { useDevices } from '@/stores/devices'
import type { KvmSession, PowerAction, PowerStatus, Sensor } from '@/api/types'
import { describe } from '@/api/client'

// Out-of-band control panel (platform-admin: power:control / kvm:access). Power
// actions, live sensor readings, and an embedded KVM console iframe from the
// short-lived kvm-session console_url.
const props = defineProps<{ deviceId: string }>()
const store = useDevices()
const ability = useAbility()
const confirm = useConfirm()
const canPower = computed(() => ability.can('control', 'Power'))
const canKvm = computed(() => ability.can('access', 'Kvm'))

const power = ref<PowerStatus | null>(null)
const sensors = ref<Sensor[]>([])
const kvm = ref<KvmSession | null>(null)
const error = ref('')
const busy = ref<string | null>(null)

const ACTIONS: { action: PowerAction; label: string; icon: string; color: 'success' | 'error' | 'warning' }[] = [
  { action: 'on', label: 'Power on', icon: 'mdi-power', color: 'success' },
  { action: 'off', label: 'Power off', icon: 'mdi-power-off', color: 'error' },
  { action: 'cycle', label: 'Power cycle', icon: 'mdi-restart', color: 'warning' },
  { action: 'reset', label: 'Reset', icon: 'mdi-restart-alert', color: 'warning' },
]
async function loadPower(): Promise<void> {
  error.value = ''
  try {
    power.value = await store.power(props.deviceId)
  } catch (e) {
    error.value = describe(e)
  }
}
async function loadSensors(): Promise<void> {
  error.value = ''
  try {
    sensors.value = await store.sensors(props.deviceId)
  } catch (e) {
    error.value = describe(e)
  }
}
async function act(a: (typeof ACTIONS)[number]): Promise<void> {
  if (a.action !== 'on' && !(await confirm.ask({ title: a.label + '?', text: 'The device loses power immediately.', danger: true, confirmLabel: a.label }))) return
  busy.value = a.action
  error.value = ''
  try {
    await store.setPower(props.deviceId, a.action)
    power.value = await store.power(props.deviceId)
  } catch (e) {
    error.value = describe(e)
  } finally {
    busy.value = null
  }
}
async function launchKvm(): Promise<void> {
  busy.value = 'kvm'
  error.value = ''
  try {
    kvm.value = await store.kvmSession(props.deviceId)
  } catch (e) {
    error.value = describe(e)
  } finally {
    busy.value = null
  }
}
const sensorRows = computed(() => sensors.value.map((s) => ({ ...s, id: s.name })))
const sensorColumns: Column<Sensor & { id: string }>[] = [
  { key: 'name', label: 'Sensor' }, { key: 'reading', label: 'Reading', format: (s) => s.reading || [s.value, s.unit].filter((x) => x !== undefined && x !== '').join(' ') }, { key: 'status', label: 'Status', width: 'sm' },
]
const sensorOk = (s: Sensor) => !s.status || s.status === 'ok' || s.status === 'nominal'
onMounted(() => {
  if (canPower.value) {
    void loadPower()
    void loadSensors()
  }
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <UiAlert v-if="error" kind="error">{{ error }}</UiAlert>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <UiCard title="Power">
        <template #header>
          <div class="flex grow items-center gap-2">
            <UiStatusChip v-if="power" :status="power.on ? 'on' : 'off'" :colors="{ on: 'success', off: 'neutral' }" />
            <UiStatusChip v-if="power?.power_fault || power?.power_overload" status="fault" label="power fault" :colors="{ fault: 'error' }" />
            <span class="grow" />
            <UiButton size="xs" variant="text" icon="mdi-refresh" icon-only label="Refresh power state" @click="loadPower" />
          </div>
        </template>
        <div v-if="canPower" class="flex flex-wrap gap-2">
          <UiButton v-for="a in ACTIONS" :key="a.action" size="sm" variant="soft" :color="a.color" :icon="a.icon" :loading="busy === a.action" :data-test="'power-' + a.action" @click="act(a)">{{ a.label }}</UiButton>
        </div>
        <p v-else class="text-sm text-base-content/70">Power control requires the platform-admin role (power:control).</p>
      </UiCard>
      <UiCard title="Sensors" :padded="false">
        <template #header>
          <div class="flex grow items-center gap-2"><span class="grow" /><UiButton size="xs" variant="text" icon="mdi-refresh" icon-only label="Refresh sensors" @click="loadSensors" /></div>
        </template>
        <UiDataTable :items="sensorRows" :columns="sensorColumns" caption="Sensors" empty-title="No sensor readings">
          <template #cell-status="{ row }"><UiStatusChip :status="sensorOk(row) ? 'ok' : 'alarm'" :label="row.status ?? 'ok'" :colors="{ ok: 'success', alarm: 'error' }" /></template>
        </UiDataTable>
      </UiCard>
    </div>
    <UiCard title="KVM console">
      <template #header>
        <div class="flex grow items-center gap-2"><span class="grow" /><UiButton v-if="canKvm" size="sm" variant="soft" icon="mdi-monitor-dashboard" :loading="busy === 'kvm'" data-test="kvm-start" @click="launchKvm">Start session</UiButton></div>
      </template>
      <div v-if="kvm">
        <iframe :src="kvm.console_url" class="aspect-video w-full rounded-box border-0 bg-black" title="KVM console" />
        <p class="mt-1 text-xs text-base-content/70">Token-gated session; expires {{ kvm.expires_at ?? 'shortly' }}.</p>
      </div>
      <p v-else class="text-sm text-base-content/70">{{ canKvm ? 'Start a session to open the out-of-band console.' : 'KVM access requires the platform-admin role (kvm:access).' }}</p>
    </UiCard>
  </div>
</template>
