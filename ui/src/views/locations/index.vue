<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { UiPage, UiAlert, UiCard, UiButton, UiTree, UiStatusChip, UiKeyValueTable, UiToolbar, UiEmptyState, UiDrawer, UiForm, UiSelect, UiNumberInput, UiRecordDrawer, useConfirm, type SelectOption, type TreeNode } from '@freya/ui'
import { useZodForm, zodToFields } from '@freya/ui/forms'
import { useLocations } from '@/stores/locations'
import { useDevices } from '@/stores/devices'
import { locationSchema, rackPlacementSchema } from '@/schemas'
import type { Device, Location, LocationTreeNode } from '@/api/types'
import { describe } from '@/api/client'
import { mergeEdit } from '@/api/merge'
import Rack from './rack.vue'

const router = useRouter()
const store = useLocations()
const devices = useDevices()
const confirm = useConfirm()
const selectedId = ref('')
const error = ref('')
const typeIcon: Record<string, string> = {
  region: 'mdi-earth', country: 'mdi-flag', city: 'mdi-city', datacenter: 'mdi-server',
  building: 'mdi-office-building', floor: 'mdi-layers', room: 'mdi-door', rack: 'mdi-server',
  site: 'mdi-map-marker', branch: 'mdi-source-branch',
}
// The type a new child most likely has, following the region → rack hierarchy.
const childType: Record<string, string> = { region: 'country', country: 'city', city: 'datacenter', site: 'building', branch: 'building', datacenter: 'room', building: 'floor', floor: 'room', room: 'rack' }

onMounted(() => {
  void refresh()
  void devices.list()
})
async function refresh(): Promise<void> {
  await Promise.all([store.loadTree(), store.list()])
}
const toNode = (l: LocationTreeNode): TreeNode => ({ id: l.id, label: l.name, icon: typeIcon[l.location_type] ?? 'mdi-map-marker', badge: l.location_type === 'rack' ? (l.rack_size_u ?? 0) + 'U' : l.device_count ? l.device_count + ' dev' : l.location_type, children: (l.children ?? []).map(toNode) })
const tree = computed<TreeNode[]>(() => store.tree.map(toNode))
const selected = computed(() => store.items.find((l) => l.id === selectedId.value) ?? null)
const isRack = computed(() => selected.value?.location_type === 'rack')
const details = computed(() => {
  const l = selected.value
  if (!l) return []
  return [
    { label: 'Type', value: l.location_type }, { label: 'Code', value: l.code }, { label: 'Path', value: l.path }, { label: 'Devices', value: l.device_count ?? 0 },
    { label: 'Subnets', value: l.subnet_count ?? 0 }, { label: 'Address', value: [l.address, l.city, l.country].filter(Boolean).join(', ') }, ...(l.location_type === 'rack' ? [{ label: 'Rack size', value: (l.rack_size_u ?? 0) + 'U' }] : []),
  ]
})

// --- create / edit / delete ---
const dialog = ref(false)
const editing = ref<Location | null>(null)
const parentFor = ref<Location | null>(null)
const locationKeys = Object.keys(locationSchema.shape)
const fields = computed(() =>
  zodToFields(locationSchema, {
    name: { cols: 6 },
    location_type: { label: 'Type', cols: 6 },
    code: { cols: 6 },
    parent_id: { label: 'Parent', type: 'select', cols: 6, options: store.items.filter((l) => l.id !== editing.value?.id).map((l) => ({ title: `${l.name} (${l.location_type})`, value: l.id })) },
    status: { cols: 6 },
    rack_size_u: { label: 'Rack height (U)', cols: 6, hint: 'Racks only — e.g. 42.' },
    address: { cols: 12 },
    city: { cols: 6 },
    country: { cols: 6 },
  }),
)
function add(parent: Location | null = null): void {
  editing.value = null
  parentFor.value = parent
  dialog.value = true
}
function edit(l: Location): void {
  editing.value = l
  parentFor.value = null
  dialog.value = true
}
const initial = computed(() => {
  if (editing.value) return { ...editing.value }
  const p = parentFor.value
  const type = p ? childType[p.location_type] ?? 'room' : 'region'
  return { parent_id: p?.id, location_type: type, status: 'active', rack_size_u: type === 'rack' ? 42 : 0 }
})
async function submit(v: Record<string, unknown>): Promise<Location> {
  const l = editing.value ? await store.update(editing.value.id, mergeEdit(editing.value, v, locationKeys)) : await store.create(v)
  selectedId.value = l.id
  return l
}
async function remove(l: Location): Promise<void> {
  if (!(await confirm.ask({ title: `Delete ${l.name}?`, ...(l.child_count ? { text: 'It still has child locations.' } : {}), danger: true, confirmLabel: 'Delete' }))) return
  error.value = ''
  try {
    await store.remove(l.id)
    selectedId.value = ''
    await refresh()
  } catch (e) {
    error.value = describe(e)
  }
}

// --- rack contents and placement ---
const rackDevices = ref<Device[]>([])
async function loadRack(): Promise<void> {
  if (!isRack.value || !selected.value) {
    rackDevices.value = []
    return
  }
  try {
    rackDevices.value = await devices.inRack(selected.value.id)
  } catch (e) {
    error.value = describe(e)
  }
}
watch(() => (isRack.value ? selected.value?.id : ''), loadRack)

const placing = ref(false)
const moving = ref<Device | null>(null)
const placeForm = useZodForm(rackPlacementSchema, {
  onSubmit: async (v) => {
    const rack = selected.value
    if (!rack) return
    const clash = collision(v.device_id, v.rack_position, v.device_height_u, rack.rack_size_u ?? 0)
    if (clash) {
      placeForm.setFieldError('rack_position', clash)
      return
    }
    const d = devices.items.find((x) => x.id === v.device_id) ?? rackDevices.value.find((x) => x.id === v.device_id)
    if (!d) return
    await devices.update(d.id, { ...d, rack_id: rack.id, rack_position: v.rack_position, device_height_u: v.device_height_u })
    placing.value = false
    await loadRack()
  },
})
// collision explains why a placement does not fit, or returns '' when it does.
function collision(deviceId: string, pos: number, h: number, size: number): string {
  if (pos + h - 1 > size) return `U${pos}–U${pos + h - 1} runs past the top of this ${size}U rack.`
  for (const o of rackDevices.value) {
    if (o.id === deviceId || !o.rack_position) continue
    const oTop = o.rack_position + Math.max(1, o.device_height_u ?? 1) - 1
    if (pos <= oTop && o.rack_position <= pos + h - 1) return `Overlaps ${o.name} (U${o.rack_position}–U${oTop}).`
  }
  return ''
}
const deviceOptions = computed<SelectOption[]>(() => {
  const inThisRack = new Set(rackDevices.value.map((d) => d.id))
  const pool = moving.value ? [moving.value] : devices.items.filter((d) => !inThisRack.has(d.id) || !d.rack_position)
  return pool.map((d) => ({ title: `${d.name} (${d.device_type})${d.rack_id && d.rack_id !== selected.value?.id ? ' — in another rack' : ''}`, value: d.id }))
})
function place(position: number): void {
  moving.value = null
  // A required select shows its first option, so make that the model value too.
  placeForm.reset({ device_id: deviceOptions.value[0]?.value ?? '', rack_position: position, device_height_u: 1 })
  placing.value = true
}
function editPlacement(d: Device): void {
  moving.value = d
  placeForm.reset({ device_id: d.id, rack_position: d.rack_position || 1, device_height_u: d.device_height_u || 1 })
  placing.value = true
}
async function unrack(d: Device): Promise<void> {
  if (!(await confirm.ask({ title: `Remove ${d.name} from this rack?`, confirmLabel: 'Remove' }))) return
  error.value = ''
  try {
    await devices.update(d.id, { ...d, rack_id: '', rack_position: 0 })
    placing.value = false
    await loadRack()
  } catch (e) {
    error.value = describe(e)
  }
}
</script>

<template>
  <UiPage title="Locations">
    <template #actions>
      <UiButton icon="mdi-plus" data-test="location-new" @click="add()">New location</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="refresh" />
    </template>
    <UiAlert v-if="store.error || error" kind="error" class="mb-3">{{ error || store.error }}</UiAlert>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-12">
      <UiCard title="Tree" class="lg:col-span-4">
        <UiEmptyState v-if="!tree.length" title="No locations yet" text="Start with a region or site, then add buildings, rooms and racks." />
        <UiTree v-else v-model:selected="selectedId" :items="tree" />
      </UiCard>
      <UiCard class="lg:col-span-8" :title="selected?.name ?? 'Location'">
        <template v-if="selected" #header><UiStatusChip :status="selected.status" :colors="{ planned: 'info', decommissioned: 'neutral' }" /></template>
        <p v-if="!selected" class="text-sm text-base-content/70">Select a location to view it, add a child or place devices in a rack.</p>
        <template v-else>
          <UiToolbar class="mb-3">
            <UiButton v-if="!isRack" size="sm" variant="soft" icon="mdi-plus" @click="add(selected)">Add child</UiButton>
            <UiButton size="sm" variant="soft" icon="mdi-pencil-outline" @click="edit(selected)">Edit</UiButton>
            <UiButton size="sm" variant="text" color="error" icon="mdi-delete-outline" @click="remove(selected)">Delete</UiButton>
          </UiToolbar>
          <p v-if="selected.description" class="mb-3 text-sm">{{ selected.description }}</p>
          <UiKeyValueTable :columns="2" :items="details" />
          <div v-if="isRack" class="mt-5">
            <h3 class="mb-2 text-sm font-medium">Rack elevation</h3>
            <Rack v-if="(selected.rack_size_u ?? 0) > 0" :size-u="selected.rack_size_u!" :devices="rackDevices" @place="place" @open="editPlacement" @unrack="unrack" />
            <p v-else class="text-sm text-base-content/70">Set the rack height (Edit → Rack height) to draw its elevation.</p>
          </div>
        </template>
      </UiCard>
    </div>

    <UiRecordDrawer v-model="dialog" close-on-save :title="editing ? `Edit ${editing.name}` : parentFor ? `New location in ${parentFor.name}` : 'New location'" :schema="locationSchema" :fields="fields" :initial="initial" :submit="submit" size="lg" @saved="refresh" />

    <UiDrawer :model-value="placing" :title="moving ? `Move ${moving.name}` : 'Place a device'" size="md" @update:model-value="placing = false">
      <UiForm v-if="placing" :form="placeForm">
        <div class="flex flex-col gap-3">
          <UiSelect v-bind="placeForm.field('device_id')" label="Device" :options="deviceOptions" :clearable="false" :disabled="!!moving" required />
          <div class="grid grid-cols-2 gap-3">
            <UiNumberInput v-bind="placeForm.field('rack_position')" label="Bottom unit (U)" :min="1" :max="selected?.rack_size_u" required />
            <UiNumberInput v-bind="placeForm.field('device_height_u')" label="Height (U)" :min="1" :max="50" required />
          </div>
        </div>
      </UiForm>
      <template #actions>
        <UiButton v-if="moving" variant="text" icon="mdi-open-in-new" class="me-auto" @click="router.push({ name: 'ipam-device', params: { id: moving.id } })">Open device</UiButton>
        <UiButton v-if="moving" variant="text" color="error" @click="unrack(moving)">Remove from rack</UiButton>
        <UiButton variant="text" color="neutral" @click="placing = false">Cancel</UiButton>
        <UiButton :loading="placeForm.submitting.value" data-test="rack-place-save" @click="placeForm.submit()">{{ moving ? 'Save' : 'Place' }}</UiButton>
      </template>
    </UiDrawer>
  </UiPage>
</template>
