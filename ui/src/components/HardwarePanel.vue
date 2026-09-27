<script setup lang="ts">
// Hardware of a host-reported device (feature 023): BIOS, system / board /
// chassis, processors, memory with every slot and the disks with their
// filesystems. Reported data, read-only; every string is rendered as text.
import { computed, onMounted, ref } from 'vue'
import { UiAlert, UiBadge, UiCard, UiDataTable, UiEmptyState, UiKeyValueTable, type Column, type KeyValue } from '@go-tangra/ui'
import { useDevices } from '@/stores/devices'
import { describe } from '@/api/client'
import type { DeviceHardware, HardwareDisk, HardwareFilesystem, HardwareMemorySlot, HardwareProcessor } from '@/api/types'
import { formatDiskSize, formatMemory, mediaLabels } from '@/views/devices/hardware'

const props = defineProps<{ deviceId: string; hasHardware: boolean }>()
const store = useDevices()
const hw = ref<DeviceHardware | null>(null)
const error = ref('')

onMounted(async () => {
  if (!props.hasHardware) return
  try {
    hw.value = await store.hardware(props.deviceId)
  } catch (e) {
    error.value = describe(e)
  }
})

const kv = (items: [string, string | undefined][]): KeyValue[] => items.filter(([, v]) => !!v).map(([label, value]) => ({ label, value: value ?? '' }))
const bios = computed(() => kv([['Vendor', hw.value?.bios?.vendor], ['Version', hw.value?.bios?.version], ['Release date', hw.value?.bios?.release_date]]))
const system = computed(() => {
  const h = hw.value
  return kv([
    ['System', [h?.system?.manufacturer, h?.system?.product, h?.system?.version].filter(Boolean).join(' ')],
    ['System serial', h?.system?.serial], ['UUID', h?.system?.uuid], ['SKU', h?.system?.sku], ['Family', h?.system?.family],
    ['Board', [h?.board?.manufacturer, h?.board?.product].filter(Boolean).join(' ')], ['Board serial', h?.board?.serial],
    ['Chassis', [h?.chassis?.type, h?.chassis?.manufacturer].filter(Boolean).join(' · ')], ['Chassis serial', h?.chassis?.serial], ['Asset tag', h?.chassis?.asset_tag],
  ])
})
const memory = computed(() => {
  const m = hw.value?.memory
  return kv([
    ['Total', formatMemory(m?.total_bytes)], ['Slots', m?.slots_total ? `${m.slots_used ?? 0} of ${m.slots_total} populated` : ''],
    ['Error correction', m?.error_correction], ['Array', [m?.location, m?.use].filter(Boolean).join(' · ')], ['Maximum capacity', formatMemory(m?.max_capacity_bytes)],
  ])
})

type Row<T> = T & { id: string }
const withIds = <T,>(items: T[] | undefined, key: (t: T, i: number) => string): Row<T>[] => (items ?? []).map((t, i) => ({ ...t, id: key(t, i) }))
const procRows = computed(() => withIds(hw.value?.processors, (p, i) => (p.socket || '') + '#' + i))
const slotRows = computed(() => withIds(hw.value?.memory?.slots, (s, i) => s.locator || '#' + i))
const diskRows = computed(() => withIds(hw.value?.disks, (d, i) => (d.name || '') + '#' + i))
const fsRows = computed(() => withIds(hw.value?.filesystems, (f, i) => (f.mount || '') + '#' + i))

const procColumns: Column<Row<HardwareProcessor>>[] = [
  { key: 'socket', label: 'Socket', width: 'sm' },
  { key: 'model', label: 'Model', format: (p) => (p.populated === false ? 'empty' : p.model || p.family || '') },
  { key: 'cores', label: 'Cores / threads', format: (p) => (p.cores ? `${p.cores} / ${p.threads ?? 0}` : '') },
  { key: 'max_mhz', label: 'Speed', hideOnStack: true, format: (p) => [p.current_mhz, p.max_mhz].filter(Boolean).map((v) => `${v} MHz`).join(' / ') },
]
const slotColumns: Column<Row<HardwareMemorySlot>>[] = [
  { key: 'locator', label: 'Slot' },
  { key: 'size_bytes', label: 'Module', format: (s) => (s.populated ? [formatMemory(s.size_bytes), s.type, s.form_factor].filter(Boolean).join(' ') : 'empty') },
  { key: 'type_detail', label: 'Detail', hideOnStack: true, format: (s) => (s.type_detail ?? []).join(', ') },
  { key: 'speed_mts', label: 'Speed', hideOnStack: true, format: (s) => (s.speed_mts ? `${s.speed_mts} MT/s` + (s.configured_mts && s.configured_mts !== s.speed_mts ? ` (configured ${s.configured_mts})` : '') : '') },
  { key: 'manufacturer', label: 'Part', hideOnStack: true, format: (s) => [s.manufacturer, s.part_number].filter(Boolean).join(' ') },
  { key: 'serial', label: 'Serial', hideOnStack: true },
]
const diskColumns: Column<Row<HardwareDisk>>[] = [
  { key: 'name', label: 'Disk' },
  { key: 'model', label: 'Model', format: (d) => [d.vendor && !(d.model ?? '').startsWith(d.vendor) ? d.vendor : '', d.model].filter(Boolean).join(' ') },
  { key: 'size_bytes', label: 'Size', format: (d) => formatDiskSize(d.size_bytes) },
  { key: 'media', label: 'Media', width: 'sm', format: (d) => mediaLabels[d.media ?? 'unknown'] ?? d.media ?? '' },
  { key: 'interface', label: 'Interface', width: 'sm', hideOnStack: true },
  { key: 'serial', label: 'Serial', hideOnStack: true },
]
const fsColumns: Column<Row<HardwareFilesystem>>[] = [
  { key: 'mount', label: 'Mount' }, { key: 'fs', label: 'FS', width: 'sm', hideOnStack: true },
  { key: 'size_bytes', label: 'Usage' }, { key: 'disks', label: 'Disks', hideOnStack: true, format: (f) => (f.disks ?? []).join(', ') },
]
const used = (f: HardwareFilesystem) => (f.size_bytes ? Math.round((100 * (f.size_bytes - (f.free_bytes ?? 0))) / f.size_bytes) : 0)
const reportedAt = computed(() => (hw.value ? new Date(hw.value.reported_at).toLocaleString() : ''))
</script>

<template>
  <div data-test="hardware-panel">
    <UiEmptyState v-if="!hasHardware" data-test="hardware-empty" icon="mdi-chip" title="No hardware reported" text="Hardware is reported by inventory agents 4.4.0 or newer; it appears after the host's next report." />
    <UiAlert v-else-if="error" kind="error">{{ error }}</UiAlert>
    <template v-else-if="hw">
      <p class="mb-2 text-sm text-base-content/70">Reported by the inventory agent · {{ reportedAt }}</p>
      <div class="mb-3 grid gap-3 md:grid-cols-2">
        <UiCard title="BIOS"><UiKeyValueTable :items="bios" /></UiCard>
        <UiCard title="System / board / chassis"><UiKeyValueTable :items="system" /></UiCard>
      </div>
      <UiCard title="Processors" :padded="false" class="mb-3">
        <UiDataTable :items="procRows" :columns="procColumns" caption="Processors" empty-title="No processors reported" />
      </UiCard>
      <UiCard title="Memory" class="mb-3">
        <UiKeyValueTable :items="memory" :columns="2" class="mb-2" />
        <UiDataTable :items="slotRows" :columns="slotColumns" caption="Memory slots" empty-title="No slots reported" :row-attrs="(s) => ({ 'data-test': 'slot-row-' + (s.locator ?? ''), class: s.populated ? '' : 'opacity-50' })" />
      </UiCard>
      <UiCard title="Disks" :padded="false" class="mb-3">
        <UiDataTable :items="diskRows" :columns="diskColumns" caption="Disks" empty-title="No disks reported">
          <template #cell-name="{ row }">{{ row.name }} <UiBadge v-if="row.removable" color="neutral" size="xs">removable</UiBadge></template>
        </UiDataTable>
      </UiCard>
      <UiCard title="Filesystems" :padded="false">
        <UiDataTable :items="fsRows" :columns="fsColumns" caption="Filesystems" empty-title="No filesystems reported">
          <template #cell-size_bytes="{ row }">
            <div class="flex items-center gap-2">
              <progress class="progress w-24" :class="used(row) >= 90 ? 'progress-error' : used(row) >= 75 ? 'progress-warning' : 'progress-success'" :value="used(row)" max="100" :aria-label="`${row.mount} ${used(row)} % used`" />
              <span class="text-xs">{{ formatDiskSize((row.size_bytes ?? 0) - (row.free_bytes ?? 0)) || '0 B' }} of {{ formatDiskSize(row.size_bytes) }}</span>
            </div>
          </template>
        </UiDataTable>
      </UiCard>
    </template>
  </div>
</template>
