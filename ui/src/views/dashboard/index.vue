<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { UiPage, UiCard, UiStatGrid, UiStatTile, UiBarList, type BarItem } from '@go-tangra/ui'
import { useStats } from '@/stores/stats'
import { useSubnets } from '@/stores/subnets'
import { useDevices } from '@/stores/devices'
import { useScans } from '@/stores/scans'
import type { Device } from '@/api/types'

// The dashboard prefers the /stats tenant snapshot; when it is unavailable it
// derives figures from list totals and the device list.
const stats = useStats()
const subnets = useSubnets()
const devices = useDevices()
const scans = useScans()
const allDevices = ref<Device[]>([])
const scanCounts = ref({ active: 0, completed: 0 })
async function loadScanCounts(): Promise<void> {
  try {
    const [pending, scanning, completed] = await Promise.all([scans.count({ status: 'pending' }), scans.count({ status: 'scanning' }), scans.count({ status: 'completed' })])
    scanCounts.value = { active: pending + scanning, completed }
  } catch {
    // the scan tiles stay at zero
  }
}
onMounted(() => {
  void stats.load()
  void subnets.list({}, { page: 1, page_size: 1, sort: 'cidr', order: 'asc' })
  void devices.lookup().then((d) => (allDevices.value = d), () => {})
  void loadScanCounts()
})
const snap = computed(() => stats.snapshot)
function tally(pick: (d: Device) => string | undefined): Record<string, number> {
  const m: Record<string, number> = {}
  for (const d of allDevices.value) {
    const k = pick(d) || 'unknown'
    m[k] = (m[k] ?? 0) + 1
  }
  return m
}
const bars = (m: Record<string, number>, color: NonNullable<BarItem['color']>): BarItem[] => Object.entries(m).sort((a, b) => b[1] - a[1]).map(([label, value]) => ({ label, value, color }))
const subnetsTotal = computed(() => snap.value?.total_subnets ?? subnets.total)
const devicesTotal = computed(() => snap.value?.total_devices ?? allDevices.value.length)
const addressesUsed = computed(() => snap.value?.used_addresses ?? 0)
const addressesTotal = computed(() => snap.value?.total_addresses ?? 0)
const securityUpdates = computed(() => allDevices.value.reduce((n, d) => n + (d.security_update_count ?? 0), 0))
const scansActive = computed(() => scanCounts.value.active)
const scansCompleted = computed(() => scanCounts.value.completed)
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
