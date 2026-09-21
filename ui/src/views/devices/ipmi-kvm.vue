<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useDevices } from '@/stores/devices'
import type { KvmSession, PowerAction, PowerStatus, Sensor } from '@/api/types'
import { describe } from '@/api/client'

// Out-of-band control panel (platform-admin: power:control / kvm:access). Power
// actions, live sensor readings, and an embedded KVM console iframe from the
// short-lived kvm-session console_url.
const props = defineProps<{ deviceId: string }>()
const store = useDevices()

const power = ref<PowerStatus | null>(null)
const sensors = ref<Sensor[]>([])
const kvm = ref<KvmSession | null>(null)
const error = ref('')
const busy = ref<string | null>(null)

const ACTIONS: { action: PowerAction; label: string; icon: string; color: string }[] = [
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
    sensors.value = (await store.sensors(props.deviceId)).sensors ?? []
  } catch (e) {
    error.value = describe(e)
  }
}

async function act(action: PowerAction): Promise<void> {
  busy.value = action
  error.value = ''
  try {
    power.value = await store.setPower(props.deviceId, action)
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

function sensorColor(s: Sensor): string {
  if (s.status && s.status !== 'ok' && s.status !== 'nominal') return 'error'
  return 'success'
}

onMounted(() => {
  void loadPower()
  void loadSensors()
})
</script>

<template>
  <div>
    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>

    <v-row>
      <v-col cols="12" md="6">
        <v-card variant="tonal">
          <v-card-title class="text-subtitle-1 d-flex align-center">
            Power
            <v-chip v-if="power" size="x-small" :color="power.chassis_on ? 'success' : 'grey'" variant="flat" class="ms-3">
              {{ power.power_state ?? (power.chassis_on ? 'on' : 'off') }}
            </v-chip>
            <v-spacer />
            <v-btn size="x-small" variant="text" icon="mdi-refresh" @click="loadPower" />
          </v-card-title>
          <v-card-text>
            <v-btn
              v-for="a in ACTIONS" :key="a.action"
              size="small" variant="tonal" :color="a.color" :prepend-icon="a.icon"
              :loading="busy === a.action" class="me-2 mb-2"
              @click="act(a.action)"
            >{{ a.label }}</v-btn>
            <div class="text-caption text-medium-emphasis mt-2">Requires the platform-admin role (power:control).</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="6">
        <v-card variant="tonal">
          <v-card-title class="text-subtitle-1 d-flex align-center">
            Sensors
            <v-spacer />
            <v-btn size="x-small" variant="text" icon="mdi-refresh" @click="loadSensors" />
          </v-card-title>
          <v-card-text class="pt-0">
            <v-table density="compact">
              <tbody>
                <tr v-for="s in sensors" :key="s.name">
                  <td class="text-medium-emphasis">{{ s.name }}</td>
                  <td>{{ s.reading }} {{ s.unit }}</td>
                  <td><v-chip size="x-small" :color="sensorColor(s)" variant="tonal">{{ s.status ?? 'ok' }}</v-chip></td>
                </tr>
              </tbody>
            </v-table>
            <div v-if="!sensors.length" class="text-medium-emphasis">No sensor readings.</div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-card variant="tonal" class="mt-4">
      <v-card-title class="text-subtitle-1 d-flex align-center">
        KVM console
        <v-spacer />
        <v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-monitor-dashboard" :loading="busy === 'kvm'" @click="launchKvm">
          Start session
        </v-btn>
      </v-card-title>
      <v-card-text>
        <div v-if="kvm">
          <iframe :src="kvm.console_url" class="kvm-frame" title="KVM console" />
          <div class="text-caption text-medium-emphasis mt-1">Token-gated session; expires {{ kvm.expires_at ?? 'shortly' }}.</div>
        </div>
        <div v-else class="text-medium-emphasis">Start a session to open the out-of-band console (kvm:access).</div>
      </v-card-text>
    </v-card>
  </div>
</template>

<style scoped>
.kvm-frame { width: 100%; height: 480px; border: 0; border-radius: 4px; background: #000; }
</style>
