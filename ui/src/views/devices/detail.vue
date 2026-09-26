<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAbility } from '@casl/vue'
import { UiPage, UiAlert, UiCard, UiButton, UiStatusChip, UiKeyValueTable, UiDataTable, UiTabs, UiBadge, UiRecordDrawer, UiSwitch, type Column, type KeyValue, type TabItem } from '@go-tangra/ui'
import { useDevices } from '@/stores/devices'
import { useHostSync } from '@/stores/hostsync'
import type { Device, DeviceHostSync, DeviceInterface, DevicePackage, HypervisorGuest, IPAddress } from '@/api/types'
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
const canResync = computed(() => ability.can('resync', 'HostSync'))
const hostSync = useHostSync()

const id = String(route.params.id)
const device = ref<Device | null>(null)
const error = ref('')
const tab = ref('interfaces')
const interfaces = ref<DeviceInterface[]>([])
const packages = ref<DevicePackage[]>([])
const addresses = ref<IPAddress[]>([])
const guests = ref<HypervisorGuest[]>([])
const report = ref<DeviceHostSync | null>(null)
const hypervisorName = ref('')
const syncing = ref(false)
const resyncing = ref(false)
const resyncMessage = ref('')
const securityOnly = ref(false)

async function loadAll(): Promise<void> {
  error.value = ''
  try {
    const d = await store.get(id)
    device.value = d
    ;[interfaces.value, packages.value, addresses.value] = await Promise.all([store.interfaces(id), store.packages(id), store.addresses(id)])
    report.value = d.source === 'host_report' ? await hostSync.device(id) : null
    guests.value = (d.guest_count ?? 0) > 0 ? await hostSync.guests(id) : []
    hypervisorName.value = d.hypervisor_device_id ? (await store.get(d.hypervisor_device_id)).name : ''
  } catch (e) {
    error.value = describe(e)
  }
}
// Applies the host's latest report now (the next report would anyway).
async function resync(): Promise<void> {
  resyncing.value = true
  error.value = ''
  resyncMessage.value = ''
  try {
    const r = await hostSync.resyncDevice(id)
    resyncMessage.value = r.changes ? `Re-synced: ${r.changes} change(s) applied` : 'Re-synced: already up to date'
    await loadAll()
  } catch (e) {
    error.value = describe(e)
  } finally {
    resyncing.value = false
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

const when = (t?: string) => (t ? new Date(t).toLocaleString() : '')
const sourceLabel: Record<string, string> = { manual: 'manual', scan: 'discovered by scan', host_report: 'host report (inventory agent)' }
// Unknown is never shown as "up to date" (US4 scenario 3).
const updateColors = { up_to_date: 'success', updates_available: 'warning', unsupported: 'neutral', error: 'error', unknown: 'neutral' } as const
const summary = computed<KeyValue[]>(() => {
  const d = device.value
  if (!d) return []
  const reported: KeyValue[] = d.source === 'host_report'
    ? [
        { label: 'Inventory host', value: d.inventory_host_id ?? '', copyable: true },
        { label: 'Last report', value: when(d.last_report_at) + (d.report_state === 'not_reported' ? ' (no longer reported)' : '') },
        { label: 'Reboot required', value: d.reboot_required ? 'yes' : 'no' },
        { label: 'Automatic updates', value: d.unattended_upgrades ? 'enabled' : 'disabled' },
      ]
    : []
  return [
    { label: 'Source', value: sourceLabel[d.source ?? 'manual'] ?? d.source ?? '' },
    ...reported,
    ...(d.virtualization_kind ? [{ label: 'Virtualization', value: d.virtualization_kind }] : []),
    ...(d.hypervisor_device_id ? [{ label: 'Runs on', value: hypervisorName.value || d.hypervisor_device_id }] : []),
    { label: 'Type', value: d.device_type }, { label: 'Manufacturer', value: d.manufacturer }, { label: 'Model', value: d.model }, { label: 'Management IP', value: d.management_ip, copyable: true },
    { label: 'OS', value: [d.os_type, d.os_version].filter(Boolean).join(' ') }, { label: 'Firmware', value: d.firmware_version }, { label: 'Location', value: locName(d.location_id) }, { label: 'Rack', value: d.rack_id ? `${locName(d.rack_id)} · U${d.rack_position ?? '?'}${(d.device_height_u ?? 1) > 1 ? '–U' + ((d.rack_position ?? 0) + (d.device_height_u ?? 1) - 1) : ''}` : '' }, { label: 'BMC', value: d.ipmi_secret_ref ? 'configured' : 'none' },
  ]
})
const tabs = computed<TabItem[]>(() => [
  { key: 'interfaces', label: 'Interfaces', count: interfaces.value.length },
  { key: 'packages', label: 'Packages', count: packages.value.length },
  { key: 'addresses', label: 'Addresses', count: addresses.value.length },
  ...(guests.value.length ? [{ key: 'guests', label: 'Guests', count: guests.value.length }] : []),
  ...(canOob.value ? [{ key: 'oob', label: 'Power / KVM' }] : []),
])
// "Connected to" (US5): the switch and port a host interface is linked to, or
// on a switch port the device behind it; otherwise the raw neighbour.
function connectedTo(i: DeviceInterface): string {
  if (i.behind_device_name) return i.behind_device_name
  if (i.remote_device_name || i.remote_interface_id) {
    return [i.remote_device_name, i.remote_port_name].filter(Boolean).join(' ') + (i.link_vlan ? ` (VLAN ${i.link_vlan})` : '') + (i.link_source ? ` · ${i.link_source === 'lldp' ? 'LLDP' : 'MAC table'}` : '')
  }
  return i.remote_port_name ?? ''
}
const ifaceColumns: Column<DeviceInterface>[] = [
  { key: 'name', label: 'Name' }, { key: 'mac_address', label: 'MAC', hideOnStack: true }, { key: 'interface_type', label: 'Kind', hideOnStack: true },
  { key: 'speed_mbps', label: 'Speed', format: (i) => (i.speed_mbps ? i.speed_mbps + ' Mbps' : '') }, { key: 'connected', label: 'Connected to', format: connectedTo },
]
const guestColumns: Column<HypervisorGuest & { id: string }>[] = [
  { key: 'name', label: 'Guest' }, { key: 'guest_ref', label: 'ID', width: 'sm' }, { key: 'kind', label: 'Kind', width: 'sm' },
  { key: 'macs', label: 'MACs', hideOnStack: true, format: (g) => (g.macs ?? []).join(', ') },
]
const guestRows = computed(() => guests.value.map((g) => ({ ...g, id: g.guest_ref })))
const pkgRows = computed(() => packages.value.filter((p) => !securityOnly.value || p.is_security_update).map((p) => ({ ...p, id: p.name })))
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
    <UiAlert v-if="resyncMessage" kind="success" class="mb-3">{{ resyncMessage }}</UiAlert>
    <UiCard v-if="device" class="mb-4">
      <UiKeyValueTable :items="summary" :columns="2" />
      <div v-if="device.source === 'host_report'" class="mt-3 flex flex-wrap items-center gap-2" data-test="device-hostsync">
        <span class="text-sm">Updates</span>
        <UiStatusChip :status="device.update_status ?? 'unknown'" :colors="updateColors" data-test="device-update-status" />
        <UiBadge v-if="device.reboot_required" color="warning" size="xs">reboot required</UiBadge>
        <span v-if="report" class="text-sm text-base-content/70">last applied {{ when(report.applied_at) }}<template v-if="report.trigger"> · {{ report.trigger }}</template></span>
        <UiButton v-if="canResync" size="xs" variant="soft" icon="mdi-sync" :loading="resyncing" data-test="device-resync" @click="resync">Re-sync</UiButton>
      </div>
      <ul v-if="report && report.issues.length" class="mt-2 list-inside list-disc text-sm text-warning" data-test="device-issues">
        <li v-for="i in report.issues" :key="i.field + i.reason">{{ i.field }}: {{ i.reason }} ({{ i.count }})</li>
      </ul>
    </UiCard>
    <UiTabs v-model="tab" :tabs="tabs" class="mb-3" />
    <UiCard v-if="tab === 'interfaces'" :padded="false">
      <UiDataTable :items="interfaces" :columns="ifaceColumns" caption="Interfaces" empty-title="No interfaces">
        <template #cell-name="{ row }">{{ row.name }} <UiBadge v-if="row.interface_type === 'management'" color="info" size="xs">BMC</UiBadge> <UiBadge v-if="row.report_state === 'not_reported'" color="neutral" size="xs">not reported</UiBadge></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'packages'" :padded="false">
      <div class="flex items-center justify-end gap-3 p-2">
        <UiSwitch id="pkg-security-only" v-model="securityOnly" label="Security updates only" />
        <UiButton size="sm" variant="soft" icon="mdi-refresh" :loading="syncing" @click="reloadPackages">Refresh</UiButton>
      </div>
      <UiDataTable :items="pkgRows" :columns="pkgColumns" caption="Packages" empty-title="No packages" :virtual-at="200">
        <template #cell-state="{ row }"><UiStatusChip :status="row.is_security_update ? 'security' : row.needs_update ? 'update' : 'current'" :colors="{ security: 'error', update: 'warning', current: 'success' }" /></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'addresses'" :padded="false">
      <UiDataTable :items="addresses" :columns="addrColumns" caption="Addresses" empty-title="No addresses">
        <template #cell-address="{ row }">{{ row.address }} <UiBadge v-if="row.is_primary" color="primary" size="xs">primary</UiBadge> <UiBadge v-if="row.report_state === 'not_reported'" color="neutral" size="xs">not reported</UiBadge> <UiBadge v-if="row.conflict" color="error" size="xs">conflict</UiBadge> <UiBadge v-if="row.previous_device_id" color="info" size="xs">moved</UiBadge></template>
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', dhcp: 'accent', deprecated: 'warning', offline: 'neutral' }" /></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'guests'" :padded="false" data-test="device-guests">
      <UiDataTable :items="guestRows" :columns="guestColumns" caption="Guests" empty-title="No guests">
        <template #cell-name="{ row }">
          <RouterLink v-if="row.guest_device_id" class="link" :to="{ name: 'ipam-device', params: { id: row.guest_device_id } }">{{ row.guest_device_name || row.name || row.guest_ref }}</RouterLink>
          <span v-else>{{ row.name || row.guest_ref }} <UiBadge color="neutral" size="xs">not in IPAM</UiBadge></span>
        </template>
      </UiDataTable>
    </UiCard>
    <IpmiKvm v-if="tab === 'oob' && canOob" :device-id="id" />
    <UiRecordDrawer v-model="editing" close-on-save :title="'Edit ' + (device?.name ?? 'device')" :schema="deviceSchema" :fields="fields" :initial="device ? { ...device } : undefined" :submit="saveDevice" size="lg" />
  </UiPage>
</template>
