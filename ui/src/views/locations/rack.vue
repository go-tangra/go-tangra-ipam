<script setup lang="ts">
// Front elevation of a rack: U1 at the bottom, devices drawn across the units
// they occupy (rack_position is the bottom U, device_height_u the span). Free
// units are buttons that ask the parent to place a device there; overlapping
// or out-of-range devices are flagged instead of silently hidden.
import { computed } from 'vue'
import { UiBadge, UiButton, UiStatTile } from '@go-tangra/ui'
import type { Device } from '@/api/types'

const props = defineProps<{ sizeU: number; devices: Device[] }>()
const emit = defineEmits<{ (e: 'place', position: number): void; (e: 'open', device: Device): void; (e: 'unrack', device: Device): void }>()

const height = (d: Device) => Math.max(1, d.device_height_u ?? 1)
const top = (d: Device) => (d.rack_position ?? 0) + height(d) - 1

const positioned = computed(() => props.devices.filter((d) => (d.rack_position ?? 0) >= 1 && top(d) <= props.sizeU))
const outOfRange = computed(() => props.devices.filter((d) => (d.rack_position ?? 0) >= 1 && top(d) > props.sizeU))
const unpositioned = computed(() => props.devices.filter((d) => !d.rack_position))

// occupancy[u] lists the devices covering unit u.
const occupancy = computed(() => {
  const m = new Map<number, Device[]>()
  for (const d of positioned.value) {
    for (let u = d.rack_position!; u <= top(d); u++) m.set(u, [...(m.get(u) ?? []), d])
  }
  return m
})
const conflicted = computed(() => {
  const ids = new Set<string>()
  for (const ds of occupancy.value.values()) if (ds.length > 1) ds.forEach((d) => ids.add(d.id))
  return ids
})
const usedU = computed(() => occupancy.value.size)
const freeU = computed(() => Math.max(0, props.sizeU - usedU.value))
const units = computed(() => Array.from({ length: props.sizeU }, (_, i) => props.sizeU - i))


// One row per unit, top (highest U) first. A multi-U device is drawn as
// consecutive cells merged visually: the top cell carries its name and is the
// focus target, the cells below it continue the same block.
interface Row { u: number; device?: Device; clash?: Device[]; first: boolean; last: boolean }
const rows = computed<Row[]>(() =>
  units.value.map((u) => {
    const here = occupancy.value.get(u) ?? []
    if (here.length > 1) return { u, clash: here, first: true, last: true }
    const d = here[0]
    if (!d) return { u, first: true, last: true }
    return { u, device: d, first: u === top(d), last: u === d.rack_position }
  }),
)
const cellClass = (r: Row) => [
  r.device?.status === 'active' ? 'bg-primary text-primary-content' : 'bg-neutral text-neutral-content',
  r.first ? 'rounded-t-sm' : '',
  r.last ? 'rounded-b-sm' : '',
]
</script>

<template>
  <div class="flex flex-col gap-4" data-test="rack-elevation">
    <div class="grid grid-cols-3 gap-2">
      <UiStatTile title="Size" :value="sizeU + 'U'" icon="mdi-server" />
      <UiStatTile title="Used" color="primary" :value="usedU + 'U'" icon="mdi-server-network" />
      <UiStatTile title="Free" color="success" :value="freeU + 'U'" icon="mdi-tray" />
    </div>
    <div class="flex flex-col gap-4 xl:flex-row">
      <div class="w-full max-w-sm shrink-0 rounded-box border-4 border-base-content/20 bg-base-200 p-1">
        <ol aria-label="Rack units, top to bottom">
          <li v-for="r in rows" :key="r.u" class="flex h-6 items-stretch" :class="r.last ? 'pb-px' : ''">
            <span class="w-8 shrink-0 self-center pe-1 text-end text-[10px] tabular-nums text-base-content/60" aria-hidden="true">{{ r.u }}</span>
            <button v-if="r.clash" type="button" class="grow truncate rounded-sm bg-error px-2 text-start text-xs text-error-content" :title="'U' + r.u + ' is claimed by ' + r.clash.map((d) => d.name).join(', ')" @click="emit('open', r.clash[0]!)">
              {{ r.clash.map((d) => d.name).join(' / ') }}
            </button>
            <button
              v-else-if="r.device"
              type="button"
              class="flex grow items-center justify-between gap-2 overflow-hidden px-2 text-start text-xs"
              :class="cellClass(r)"
              :tabindex="r.first ? 0 : -1"
              :aria-hidden="r.first ? undefined : 'true'"
              :aria-label="r.first ? `${r.device.name}, U${r.device.rack_position}${height(r.device) > 1 ? ' to U' + top(r.device) : ''}` : undefined"
              :data-test="r.first ? 'rack-device-' + r.device.id : undefined"
              @click="emit('open', r.device)"
            >
              <template v-if="r.first"><span class="truncate font-medium">{{ r.device.name }}</span><span class="shrink-0 opacity-80">{{ height(r.device) }}U</span></template>
            </button>
            <button v-else type="button" class="grow rounded-sm border border-dashed border-base-content/15 text-[10px] text-transparent hover:border-primary hover:bg-primary/10 hover:text-primary focus-visible:text-primary" :aria-label="'Place a device at U' + r.u" :data-test="'rack-slot-' + r.u" @click="emit('place', r.u)">+ U{{ r.u }}</button>
          </li>
        </ol>
      </div>
      <div class="flex min-w-0 grow flex-col gap-3">
        <p v-if="conflicted.size" class="text-sm text-error">{{ conflicted.size }} devices overlap — move one of them.</p>
        <section v-if="outOfRange.length || unpositioned.length">
          <h3 class="mb-1 text-sm font-medium">Not shown in the elevation</h3>
          <ul class="divide-y divide-base-300 rounded-box border border-base-300 text-sm">
            <li v-for="d in [...outOfRange, ...unpositioned]" :key="d.id" class="flex items-center gap-2 px-3 py-1.5">
              <span class="grow truncate">{{ d.name }}</span>
              <UiBadge :color="d.rack_position ? 'warning' : 'neutral'" size="xs">{{ d.rack_position ? `U${d.rack_position} exceeds ${sizeU}U` : 'no position' }}</UiBadge>
              <UiButton size="xs" variant="text" icon="mdi-pencil-outline" icon-only :label="'Edit placement of ' + d.name" @click="emit('open', d)" />
              <UiButton size="xs" variant="text" color="error" icon="mdi-tray-remove" icon-only :label="'Remove ' + d.name + ' from rack'" @click="emit('unrack', d)" />
            </li>
          </ul>
        </section>
        <section v-if="positioned.length">
          <h3 class="mb-1 text-sm font-medium">Mounted</h3>
          <ul class="divide-y divide-base-300 rounded-box border border-base-300 text-sm">
            <li v-for="d in [...positioned].sort((a, b) => top(b) - top(a))" :key="d.id" class="flex items-center gap-2 px-3 py-1.5">
              <span class="w-16 shrink-0 text-xs tabular-nums text-base-content/70">U{{ d.rack_position }}{{ height(d) > 1 ? '–' + top(d) : '' }}</span>
              <span class="grow truncate">{{ d.name }}</span>
              <UiButton size="xs" variant="text" icon="mdi-pencil-outline" icon-only :label="'Edit placement of ' + d.name" @click="emit('open', d)" />
              <UiButton size="xs" variant="text" color="error" icon="mdi-tray-remove" icon-only :label="'Remove ' + d.name + ' from rack'" @click="emit('unrack', d)" />
            </li>
          </ul>
        </section>
        <p v-if="!devices.length" class="text-sm text-base-content/70">Empty rack. Click a free unit to place a device.</p>
      </div>
    </div>
  </div>
</template>
