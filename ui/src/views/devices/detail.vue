<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAbility } from '@casl/vue'
import { UiPage, UiAlert, UiCard, UiButton, UiStatusChip, UiKeyValueTable, UiDataTable, UiTabs, UiBadge, UiRecordDrawer, type Column, type KeyValue, type TabItem } from '@freya/ui'
import { useDevices } from '@/stores/devices'
import type { Device, DeviceInterface, DevicePackage, IPAddress } from '@/api/types'
import { describe } from '@/api/client'
import { mergeEdit } from '@/api/merge'
import { deviceSchema } from '@/schemas'
import { useLocations } from '@/stores/locations'
import { useDeviceFields } from './fields'
import { statusColors } from './colors'
import IpmiKvm from './ipmi-kvm.vue'

const route = useRoute()
const router = useRouter()
const store = useDevices()
const ability = useAbility()
const canOob = computed(() => ability.can('control', 'Power') || ability.can('access', 'Kvm'))

const id = String(route.params.id)
const device = ref<Device | null>(null)
const error = ref('')
const tab = ref('interfaces')
const interfaces = ref<DeviceInterface[]>([])
const packages = ref<DevicePackage[]>([])
const addresses = ref<IPAddress[]>([])
const syncing = ref(false)

async function loadAll(): Promise<void> {
  error.value = ''
  try {
    device.value = await store.get(id)
    ;[interfaces.value, packages.value, addresses.value] = await Promise.all([store.interfaces(id), store.packages(id), store.addresses(id)])
  } catch (e) {
    error.value = describe(e)
  }
}
// Packages are reported by the device's agent; the UI only re-reads them.
async function reloadPackages(): Promise<void> {
  syncing.value = true
  error.value = ''
  try {
    packages.value = await store.packages(id)
  } catch (e) {
    error.value = describe(e)
  } finally {
    syncing.value = false
  }
}
onMounted(loadAll)

const editing = ref(false)
const fields = useDeviceFields()
const locations = useLocations()
const deviceKeys = Object.keys(deviceSchema.shape)
async function saveDevice(v: Record<string, unknown>): Promise<Device> {
  const d = await store.update(id, mergeEdit(device.value ?? {}, v, deviceKeys))
  device.value = d
  return d
}
const locName = (lid?: string) => (lid ? locations.items.find((l) => l.id === lid)?.name ?? lid : '')

const summary = computed<KeyValue[]>(() => {
  const d = device.value
  if (!d) return []
  return [
    { label: 'Type', value: d.device_type }, { label: 'Manufacturer', value: d.manufacturer }, { label: 'Model', value: d.model }, { label: 'Management IP', value: d.management_ip, copyable: true },
    { label: 'OS', value: [d.os_type, d.os_version].filter(Boolean).join(' ') }, { label: 'Firmware', value: d.firmware_version }, { label: 'Location', value: locName(d.location_id) }, { label: 'Rack', value: d.rack_id ? `${locName(d.rack_id)} · U${d.rack_position ?? '?'}${(d.device_height_u ?? 1) > 1 ? '–U' + ((d.rack_position ?? 0) + (d.device_height_u ?? 1) - 1) : ''}` : '' }, { label: 'BMC', value: d.ipmi_secret_ref ? 'configured' : 'none' },
  ]
})
const tabs = computed<TabItem[]>(() => [
  { key: 'interfaces', label: 'Interfaces', count: interfaces.value.length },
  { key: 'packages', label: 'Packages', count: packages.value.length },
  { key: 'addresses', label: 'Addresses', count: addresses.value.length },
  ...(canOob.value ? [{ key: 'oob', label: 'Power / KVM' }] : []),
])
const ifaceColumns: Column<DeviceInterface>[] = [
  { key: 'name', label: 'Name' }, { key: 'mac_address', label: 'MAC', hideOnStack: true }, { key: 'interface_type', label: 'Type', hideOnStack: true },
  { key: 'speed_mbps', label: 'Speed', format: (i) => (i.speed_mbps ? i.speed_mbps + ' Mbps' : '') }, { key: 'remote_port_name', label: 'Neighbor' },
]
const pkgRows = computed(() => packages.value.map((p) => ({ ...p, id: p.name })))
const pkgColumns: Column<DevicePackage & { id: string }>[] = [
  { key: 'name', label: 'Package', sortable: true }, { key: 'current_version', label: 'Current' }, { key: 'available_version', label: 'Available', hideOnStack: true },
  { key: 'state', label: 'Status', width: 'sm', format: (p) => (p.is_security_update ? 'security' : p.needs_update ? 'update' : 'current') },
]
const addrColumns: Column<IPAddress>[] = [{ key: 'address', label: 'Address' }, { key: 'hostname', label: 'Hostname' }, { key: 'address_type', label: 'Type', hideOnStack: true }, { key: 'status', label: 'Status', width: 'sm' }]
</script>

<template>
  <UiPage :title="device?.name ?? 'Device'">
    <template #before-title><UiButton variant="text" icon="mdi-arrow-left" icon-only label="Back to devices" @click="router.push({ name: 'ipam-devices' })" /></template>
    <template #badges><UiStatusChip v-if="device" :status="device.status" :colors="statusColors" /></template>
    <template #actions>
      <UiButton v-if="device" variant="soft" icon="mdi-pencil-outline" data-test="device-edit" @click="editing = true">Edit</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="loadAll" />
    </template>
    <UiAlert v-if="error" kind="error" class="mb-3">{{ error }}</UiAlert>
    <UiCard v-if="device" class="mb-4"><UiKeyValueTable :items="summary" :columns="2" /></UiCard>
    <UiTabs v-model="tab" :tabs="tabs" class="mb-3" />
    <UiCard v-if="tab === 'interfaces'" :padded="false"><UiDataTable :items="interfaces" :columns="ifaceColumns" caption="Interfaces" empty-title="No interfaces" /></UiCard>
    <UiCard v-if="tab === 'packages'" :padded="false">
      <div class="flex justify-end p-2"><UiButton size="sm" variant="soft" icon="mdi-refresh" :loading="syncing" @click="reloadPackages">Refresh</UiButton></div>
      <UiDataTable :items="pkgRows" :columns="pkgColumns" caption="Packages" empty-title="No packages" :virtual-at="200">
        <template #cell-state="{ row }"><UiStatusChip :status="row.is_security_update ? 'security' : row.needs_update ? 'update' : 'current'" :colors="{ security: 'error', update: 'warning', current: 'success' }" /></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'addresses'" :padded="false">
      <UiDataTable :items="addresses" :columns="addrColumns" caption="Addresses" empty-title="No addresses">
        <template #cell-address="{ row }">{{ row.address }} <UiBadge v-if="row.is_primary" color="primary" size="xs">primary</UiBadge></template>
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', dhcp: 'accent', deprecated: 'warning', offline: 'neutral' }" /></template>
      </UiDataTable>
    </UiCard>
    <IpmiKvm v-if="tab === 'oob' && canOob" :device-id="id" />
    <UiRecordDrawer v-model="editing" close-on-save :title="'Edit ' + (device?.name ?? 'device')" :schema="deviceSchema" :fields="fields" :initial="device ? { ...device } : undefined" :submit="saveDevice" size="lg" />
  </UiPage>
</template>
