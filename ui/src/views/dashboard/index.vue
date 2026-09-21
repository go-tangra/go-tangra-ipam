<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useStats } from '@/stores/stats'
import { useSubnets } from '@/stores/subnets'
import { useDevices } from '@/stores/devices'
import { useScans } from '@/stores/scans'
import StatsCard from '@/components/StatsCard.vue'

// The dashboard prefers the /stats tenant snapshot; when it is unavailable it
// derives figures from the loaded entity lists.
const stats = useStats()
const subnets = useSubnets()
const devices = useDevices()
const scans = useScans()

onMounted(() => {
  void stats.load()
  void subnets.list()
  void devices.list()
  void scans.list()
})

const snap = computed(() => stats.snapshot)

function tally(pick: (d: (typeof devices.items)[number]) => string | undefined): Record<string, number> {
  const m: Record<string, number> = {}
  for (const d of devices.items) {
    const k = pick(d) || 'unknown'
    m[k] = (m[k] ?? 0) + 1
  }
  return m
}

const subnetsTotal = computed(() => snap.value?.subnets_total ?? subnets.items.length)
const devicesTotal = computed(() => snap.value?.devices_total ?? devices.items.length)
const addressesUsed = computed(() => snap.value?.addresses_used ?? 0)
const addressesTotal = computed(() => snap.value?.addresses_total ?? 0)
const securityUpdates = computed(() => snap.value?.security_updates ?? devices.items.reduce((n, d) => n + (d.security_update_count ?? 0), 0))
const scansActive = computed(() => snap.value?.scans_active ?? scans.items.filter((s) => s.status === 'scanning' || s.status === 'pending').length)
const scansCompleted = computed(() => snap.value?.scans_completed ?? scans.items.filter((s) => s.status === 'completed').length)

const utilization = computed(() => {
  if (typeof snap.value?.utilization === 'number') {
    const u = snap.value.utilization
    return Math.round(u <= 1 ? u * 100 : u)
  }
  return addressesTotal.value > 0 ? Math.round((addressesUsed.value / addressesTotal.value) * 100) : 0
})
const utilColor = computed(() => (utilization.value >= 90 ? 'error' : utilization.value >= 75 ? 'warning' : 'success'))

const byType = computed<[string, number][]>(() => Object.entries(snap.value?.devices_by_type ?? tally((d) => d.device_type)).sort((a, b) => b[1] - a[1]))
const byStatus = computed<[string, number][]>(() => Object.entries(snap.value?.devices_by_status ?? tally((d) => d.status)).sort((a, b) => b[1] - a[1]))
const typeMax = computed(() => Math.max(1, ...byType.value.map(([, n]) => n)))
const statusMax = computed(() => Math.max(1, ...byStatus.value.map(([, n]) => n)))
</script>

<template>
  <div>
    <h1 class="text-h5 mb-4">IPAM</h1>

    <v-row>
      <v-col cols="12" sm="6" md="3"><StatsCard title="Subnets" :value="subnetsTotal" icon="mdi-ip-network" color="primary" /></v-col>
      <v-col cols="12" sm="6" md="3"><StatsCard title="Devices" :value="devicesTotal" icon="mdi-server-network" color="info" /></v-col>
      <v-col cols="12" sm="6" md="3"><StatsCard title="Addresses in use" :value="addressesUsed" icon="mdi-ip" color="teal" :subtitle="addressesTotal + ' total'" /></v-col>
      <v-col cols="12" sm="6" md="3"><StatsCard title="Security updates" :value="securityUpdates" icon="mdi-shield-alert-outline" color="error" /></v-col>
    </v-row>

    <v-row class="mt-2">
      <v-col cols="12" md="4">
        <v-card>
          <v-card-title class="text-subtitle-1">Address utilization</v-card-title>
          <v-card-text>
            <div class="d-flex align-center mb-2">
              <v-progress-linear :model-value="utilization" height="14" rounded :color="utilColor" />
              <span class="ms-3 text-body-1 font-weight-bold">{{ utilization }}%</span>
            </div>
            <div class="text-caption text-medium-emphasis">{{ addressesUsed }} of {{ addressesTotal }} addresses allocated.</div>
          </v-card-text>
        </v-card>

        <v-card class="mt-4">
          <v-card-title class="text-subtitle-1">Scan activity</v-card-title>
          <v-card-text>
            <v-row dense>
              <v-col cols="6"><StatsCard title="Active" :value="scansActive" icon="mdi-radar" color="info" /></v-col>
              <v-col cols="6"><StatsCard title="Completed" :value="scansCompleted" icon="mdi-check-circle-outline" color="success" /></v-col>
            </v-row>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="4">
        <v-card>
          <v-card-title class="text-subtitle-1">Devices by type</v-card-title>
          <v-card-text>
            <div v-for="[t, n] in byType" :key="t" class="d-flex align-center mb-2">
              <v-chip size="x-small" variant="tonal" class="me-3" style="min-width: 120px; justify-content: center">{{ t }}</v-chip>
              <v-progress-linear :model-value="(n / typeMax) * 100" height="8" rounded color="info" />
              <span class="ms-3 text-body-2">{{ n }}</span>
            </div>
            <div v-if="!byType.length" class="text-medium-emphasis">No devices yet.</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="4">
        <v-card>
          <v-card-title class="text-subtitle-1">Devices by status</v-card-title>
          <v-card-text>
            <div v-for="[s, n] in byStatus" :key="s" class="d-flex align-center mb-2">
              <v-chip size="x-small" variant="tonal" class="me-3" style="min-width: 120px; justify-content: center">{{ s }}</v-chip>
              <v-progress-linear :model-value="(n / statusMax) * 100" height="8" rounded color="primary" />
              <span class="ms-3 text-body-2">{{ n }}</span>
            </div>
            <div v-if="!byStatus.length" class="text-medium-emphasis">No devices yet.</div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>
  </div>
</template>
