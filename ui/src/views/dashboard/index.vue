<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { UiPage, UiCard, UiStatGrid, UiStatTile, UiBarList, type BarItem } from '@go-tangra/ui'
import { useStats } from '@/stores/stats'
import { useSubnets } from '@/stores/subnets'
import { useDevices } from '@/stores/devices'
import { useScans } from '@/stores/scans'

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
const bars = (m: Record<string, number>, color: NonNullable<BarItem['color']>): BarItem[] => Object.entries(m).sort((a, b) => b[1] - a[1]).map(([label, value]) => ({ label, value, color }))
const subnetsTotal = computed(() => snap.value?.total_subnets ?? subnets.items.length)
const devicesTotal = computed(() => snap.value?.total_devices ?? devices.items.length)
const addressesUsed = computed(() => snap.value?.used_addresses ?? 0)
const addressesTotal = computed(() => snap.value?.total_addresses ?? 0)
const securityUpdates = computed(() => devices.items.reduce((n, d) => n + (d.security_update_count ?? 0), 0))
const scansActive = computed(() => scans.items.filter((s) => s.status === 'scanning' || s.status === 'pending').length)
const scansCompleted = computed(() => scans.items.filter((s) => s.status === 'completed').length)
const utilization = computed(() => {
  if (typeof snap.value?.overall_utilization === 'number') {
    const u = snap.value.overall_utilization
    return Math.round(u <= 1 ? u * 100 : u)
  }
  return addressesTotal.value > 0 ? Math.round((addressesUsed.value / addressesTotal.value) * 100) : 0
})
const utilClass = computed(() => (utilization.value >= 90 ? 'progress-error' : utilization.value >= 75 ? 'progress-warning' : 'progress-success'))
const byType = computed(() => bars(snap.value?.devices_by_type ?? tally((d) => d.device_type), 'info'))
const byStatus = computed(() => bars(tally((d) => d.status), 'primary'))
</script>

<template>
  <UiPage title="IPAM">
    <UiStatGrid class="mb-4" :cols="4">
      <UiStatTile title="Subnets" :value="subnetsTotal" icon="mdi-ip-network" color="primary" />
      <UiStatTile title="Devices" :value="devicesTotal" icon="mdi-server-network" color="info" />
      <UiStatTile title="Addresses in use" :value="addressesUsed" icon="mdi-ip" color="accent" :subtitle="addressesTotal + ' total'" />
      <UiStatTile title="Security updates" :value="securityUpdates" icon="mdi-shield-alert-outline" color="error" />
    </UiStatGrid>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-3">
      <div class="flex flex-col gap-4">
        <UiCard title="Address utilization">
          <div class="flex items-center gap-3"><progress class="progress h-3 grow" :class="utilClass" :value="utilization" max="100" aria-label="Address utilization" /><span class="font-bold">{{ utilization }}%</span></div>
          <p class="mt-1 text-xs text-base-content/70">{{ addressesUsed }} of {{ addressesTotal }} addresses allocated.</p>
        </UiCard>
        <UiCard title="Scan activity">
          <UiStatGrid :cols="2">
            <UiStatTile title="Active" :value="scansActive" icon="mdi-radar" color="info" />
            <UiStatTile title="Completed" :value="scansCompleted" icon="mdi-check-circle-outline" color="success" />
          </UiStatGrid>
        </UiCard>
      </div>
      <UiCard title="Devices by type"><UiBarList :items="byType" empty-title="No devices yet" /></UiCard>
      <UiCard title="Devices by status"><UiBarList :items="byStatus" empty-title="No devices yet" /></UiCard>
    </div>
  </UiPage>
</template>
