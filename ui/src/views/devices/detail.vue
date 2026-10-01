<script setup lang="ts">
import { computed, onMounted, ref, watch, type Ref, type UnwrapRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAbility } from '@casl/vue'
import { UiPage, UiAlert, UiCard, UiButton, UiStatusChip, UiKeyValueTable, UiDataTable, UiTabs, UiBadge, UiRecordDrawer, UiSwitch, useListQuery, type Column, type ListQueryOptions, type KeyValue, type TabItem } from '@go-tangra/ui'
import { DEVICE_ADDRESS_LIST, INTERFACE_LIST, PACKAGE_LIST, useDevices } from '@/stores/devices'
import { listOptions } from '@/stores/paged'
import type { ListParams, Page } from '@/api/list'
import { useHostSync } from '@/stores/hostsync'
import type { BmcStatus, Device, DeviceHostSync, DeviceInterface, DevicePackage, HypervisorGuest, IPAddress } from '@/api/types'
import { describe } from '@/api/client'
import { mergeEdit } from '@/api/merge'
import { deviceSchema } from '@/schemas'
import { useLocations } from '@/stores/locations'
import { useDeviceFields } from './fields'
import { statusColors } from './colors'
import IpmiKvm from './ipmi-kvm.vue'
import HardwarePanel from '@/components/HardwarePanel.vue'
import DeviceBmcCard from '@/components/DeviceBmcCard.vue'
import { hardwareSummaryLine } from './hardware'
import { addressLinkText, linksText, sortedLinks } from '@/views/addresses/mac'

const route = useRoute()
const router = useRouter()
const store = useDevices()
const ability = useAbility()
const canOob = computed(() => ability.can('control', 'Power') || ability.can('access', 'Kvm'))
const canResync = computed(() => ability.can('resync', 'HostSync'))
// The BMC credentials card (024) reads the secret's metadata from Warden as
// the viewer, so it is shown to those who manage devices or use the BMC.
const canBmc = computed(() => canOob.value || ability.can('configure', 'DeviceBmc'))
const bmcStatus = ref<BmcStatus | null>(null)
const hostSync = useHostSync()

const id = String(route.params.id)
const device = ref<Device | null>(null)
const error = ref('')
const tab = ref('interfaces')
const report = ref<DeviceHostSync | null>(null)
const hypervisorName = ref('')
const syncing = ref(false)
const resyncing = ref(false)
const resyncMessage = ref('')
const securityOnly = ref(false)

// One server-paged table of the device (page / size / sort in the URL under
// its key, e.g. ?interfaces.page=2). A superseded request's rows are ignored.
interface SubTable<T> {
  lq: ReturnType<typeof useListQuery>
  rows: Ref<UnwrapRef<T[]>>
  total: Ref<number>
  loading: Ref<boolean>
  load(): Promise<void>
}
function subTable<T>(key: string, opts: ListQueryOptions, fetch: (q: ListParams) => Promise<Page<T>>): SubTable<T> {
  const lq = useListQuery(key, opts)
  const rows = ref<T[]>([])
  const total = ref(0)
  const loading = ref(false)
  async function load(): Promise<void> {
    loading.value = true
    try {
      const res = await lq.track(fetch(lq.query.value))
      if (!res) return
      rows.value = res.items as UnwrapRef<T[]>
      total.value = res.total
      if (res.page) lq.clampTo(res.page) // a page beyond the end answers the last page
    } catch (e) {
      error.value = describe(e)
    } finally {
      loading.value = false
    }
  }
  watch(lq.query, () => void load())
  return { lq, rows, total, loading, load }
}
const interfaces = subTable<DeviceInterface>('interfaces', INTERFACE_LIST, (q) => store.interfaces(id, q))
const packages = subTable<DevicePackage>('packages', PACKAGE_LIST, (q) => store.packages(id, q, securityOnly.value ? { security_only: true } : {}))
const addresses = subTable<IPAddress>('device-addresses', DEVICE_ADDRESS_LIST, (q) => store.addresses(id, q))
const guests = subTable<HypervisorGuest>('guests', listOptions(['name'], 'name', 'asc'), (q) => hostSync.guests(id, q))
// The security filter is applied by the server: back to page 1 (which reloads).
watch(securityOnly, () => (packages.lq.page.value !== 1 ? packages.lq.resetPage() : void packages.load()))

async function loadAll(): Promise<void> {
  error.value = ''
  try {
    const d = await store.get(id)
    device.value = d
    await Promise.all([interfaces.load(), packages.load(), addresses.load(), (d.guest_count ?? 0) > 0 ? guests.load() : Promise.resolve()])
    report.value = d.source === 'host_report' ? await hostSync.device(id) : null
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
    await packages.load()
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
const updateLabels: Record<string, string> = { up_to_date: 'up to date', updates_available: 'updates available', unsupported: 'not supported', error: 'check failed', unknown: 'unknown' }
const updateLabel = computed(() => {
  const d = device.value
  const status = d?.update_status ?? 'unknown'
  const label = updateLabels[status] ?? status
  if (status !== 'updates_available' || !d) return label
  const counts = [d.package_update_count ? `${d.package_update_count} packages` : '', d.security_update_count ? `${d.security_update_count} security` : ''].filter(Boolean)
  return counts.length ? `${label} (${counts.join(', ')})` : label
})
// The stored trigger is "poll", "reconcile" or "resync:<actor>" / "resync_all:<actor>";
// the actor is in the audit log, not shown here.
function triggerLabel(t: string): string {
  const kind = t.split(':', 1)[0] ?? t
  return ({ poll: 'scheduled sync', reconcile: 'reconcile', resync: 'manual re-sync', resync_all: 're-sync of all hosts' } as Record<string, string>)[kind] ?? kind
}
// Feature 023: reported hardware at a glance (CPU, memory, disks).
const hardwareLine = computed(() => (device.value?.hardware_summary ? hardwareSummaryLine(device.value.hardware_summary) : ''))
const showHardware = computed(() => !!device.value?.hardware_summary || device.value?.source === 'host_report')
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
    ...(d.description ? [{ label: 'Description', value: d.description }] : []),
    { label: 'Source', value: sourceLabel[d.source ?? 'manual'] ?? d.source ?? '' },
    ...reported,
    ...(d.virtualization_kind ? [{ label: 'Virtualization', value: d.virtualization_kind }] : []),
    ...(d.hypervisor_device_id ? [{ label: 'Runs on', value: hypervisorName.value || d.hypervisor_device_id }] : []),
    { label: 'Type', value: d.device_type }, { label: 'Manufacturer', value: d.manufacturer }, { label: 'Model', value: d.model }, { label: 'Management IP', value: d.management_ip, copyable: true },
    { label: 'OS', value: [d.os_type, d.os_version].filter(Boolean).join(' ') }, { label: 'Firmware', value: d.firmware_version }, { label: 'Location', value: locName(d.location_id) }, { label: 'Rack', value: d.rack_id ? `${locName(d.rack_id)} · U${d.rack_position ?? '?'}${(d.device_height_u ?? 1) > 1 ? '–U' + ((d.rack_position ?? 0) + (d.device_height_u ?? 1) - 1) : ''}` : '' }, { label: 'BMC', value: bmcLabel.value },
  ]
})
// "zax-5 IPMI (Warden)" once the card read the metadata, else configured/none.
const bmcLabel = computed(() => {
  const name = bmcStatus.value?.secret?.name
  if (name) return `${name} (Warden)`
  return device.value?.ipmi_secret_ref ? 'configured (Warden)' : 'none'
})
const tabs = computed<TabItem[]>(() => [
  { key: 'interfaces', label: 'Interfaces', count: interfaces.total.value },
  { key: 'packages', label: 'Packages', count: packages.total.value },
  { key: 'addresses', label: 'Addresses', count: addresses.total.value },
  ...(guests.total.value ? [{ key: 'guests', label: 'Guests', count: guests.total.value }] : []),
  ...(showHardware.value ? [{ key: 'hardware', label: 'Hardware' }] : []),
  ...(canOob.value ? [{ key: 'oob', label: 'Power / KVM' }] : []),
])
// "Connected to" (US5): the switch and port a host interface is linked to, or
// on a switch port the device behind it; otherwise the raw neighbour.
function connectedTo(i: DeviceInterface): string {
  // 022: the addresses (agentless hosts) behind a switch port.
  const behind = (i.behind_addresses ?? []).map((a) => (a.hostname ? `${a.address} (${a.hostname})` : a.address)).join(', ')
  if (i.behind_device_name) return behind ? `${i.behind_device_name}, ${behind}` : i.behind_device_name
  if (behind) return behind
  // A host interface learned on several switches (bond): every switch port.
  if (i.links?.length) {
    const src = sortedLinks(i.links)[0]?.source
    return linksText(i.links) + ` · ${src === 'lldp' ? 'LLDP' : 'MAC table'}`
  }
  if (i.remote_device_name || i.remote_interface_id) {
    return [i.remote_device_name, i.remote_port_name].filter(Boolean).join(' ') + (i.link_vlan ? ` (VLAN ${i.link_vlan})` : '') + (i.link_source ? ` · ${i.link_source === 'lldp' ? 'LLDP' : 'MAC table'}` : '')
  }
  return i.remote_port_name ?? ''
}
// Sortable columns are the server's sort fields of each table.
const ifaceColumns: Column<DeviceInterface>[] = [
  { key: 'name', label: 'Name', sortable: true }, { key: 'mac_address', label: 'MAC', hideOnStack: true }, { key: 'interface_type', label: 'Kind', hideOnStack: true },
  { key: 'speed_mbps', label: 'Speed', format: (i) => (i.speed_mbps ? i.speed_mbps + ' Mbps' : '') }, { key: 'connected', label: 'Connected to', format: connectedTo },
]
const guestColumns: Column<HypervisorGuest & { id: string }>[] = [
  { key: 'name', label: 'Guest', sortable: true }, { key: 'guest_ref', label: 'ID', width: 'sm' }, { key: 'kind', label: 'Kind', width: 'sm' },
  { key: 'macs', label: 'MACs', hideOnStack: true, format: (g) => (g.macs ?? []).join(', ') },
]
const guestRows = computed(() => guests.rows.value.map((g) => ({ ...g, id: g.guest_ref })))
const pkgRows = computed(() => packages.rows.value.map((p) => ({ ...p, id: p.name })))
const pkgColumns: Column<DevicePackage & { id: string }>[] = [
  { key: 'name', label: 'Package', sortable: true }, { key: 'version', label: 'Current', sortable: true, format: (p) => p.current_version ?? '' }, { key: 'available_version', label: 'Available', hideOnStack: true },
  { key: 'state', label: 'Status', width: 'sm', format: (p) => (p.is_security_update ? 'security' : p.needs_update ? 'update' : 'current') },
]
const addrColumns: Column<IPAddress>[] = [{ key: 'address', label: 'Address', sortable: true }, { key: 'hostname', label: 'Hostname', sortable: true }, { key: 'link', label: 'Connected to', hideOnStack: true, format: addressLinkText }, { key: 'address_type', label: 'Type', sortable: true, hideOnStack: true }, { key: 'status', label: 'Status', width: 'sm', sortable: true }]
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
      <p v-if="hardwareLine" class="mt-3 text-sm" data-test="device-hardware-summary"><span class="font-medium">Hardware</span> · {{ hardwareLine }}</p>
      <div v-if="device.source === 'host_report'" class="mt-3 flex flex-wrap items-center gap-2" data-test="device-hostsync">
        <span class="text-sm">Updates</span>
        <UiStatusChip :status="device.update_status ?? 'unknown'" :label="updateLabel" :colors="updateColors" data-test="device-update-status" />
        <UiBadge v-if="device.reboot_required" color="warning" size="xs">reboot required</UiBadge>
        <span v-if="report" class="text-sm text-base-content/70">last applied {{ when(report.applied_at) }}<template v-if="report.trigger"> · {{ triggerLabel(report.trigger) }}</template></span>
        <UiButton v-if="canResync" size="xs" variant="soft" icon="mdi-sync" :loading="resyncing" data-test="device-resync" @click="resync">Re-sync</UiButton>
      </div>
      <ul v-if="report && report.issues.length" class="mt-2 list-inside list-disc text-sm text-warning" data-test="device-issues">
        <li v-for="i in report.issues" :key="i.field + i.reason">{{ i.field }}: {{ i.reason }} ({{ i.count }})</li>
      </ul>
    </UiCard>
    <DeviceBmcCard v-if="device && canBmc" :device-id="id" class="mb-4" @status="bmcStatus = $event" @changed="loadAll" />
    <UiTabs v-model="tab" :tabs="tabs" class="mb-3" />
    <UiCard v-if="tab === 'interfaces'" :padded="false">
      <UiDataTable :items="interfaces.rows.value" :columns="ifaceColumns" :loading="interfaces.loading.value" :total="interfaces.total.value" :page="interfaces.lq.page.value" :page-size="interfaces.lq.pageSize.value" :sort="interfaces.lq.sort.value" caption="Interfaces" empty-title="No interfaces" @update:page="interfaces.lq.setPage" @update:page-size="interfaces.lq.setPageSize" @update:sort="interfaces.lq.setSort">
        <template #cell-name="{ row }">{{ row.name }} <UiBadge v-if="row.interface_type === 'management'" color="info" size="xs">BMC</UiBadge> <UiBadge v-if="row.report_state === 'not_reported'" color="neutral" size="xs">not reported</UiBadge></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'packages'" :padded="false">
      <div class="flex items-center justify-end gap-3 p-2">
        <UiSwitch id="pkg-security-only" v-model="securityOnly" label="Security updates only" />
        <UiButton size="sm" variant="soft" icon="mdi-refresh" :loading="syncing" @click="reloadPackages">Refresh</UiButton>
      </div>
      <UiDataTable :items="pkgRows" :columns="pkgColumns" :loading="packages.loading.value" :total="packages.total.value" :page="packages.lq.page.value" :page-size="packages.lq.pageSize.value" :sort="packages.lq.sort.value" caption="Packages" empty-title="No packages" @update:page="packages.lq.setPage" @update:page-size="packages.lq.setPageSize" @update:sort="packages.lq.setSort">
        <template #cell-state="{ row }"><UiStatusChip :status="row.is_security_update ? 'security' : row.needs_update ? 'update' : 'current'" :colors="{ security: 'error', update: 'warning', current: 'success' }" /></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'addresses'" :padded="false">
      <UiDataTable :items="addresses.rows.value" :columns="addrColumns" :loading="addresses.loading.value" :total="addresses.total.value" :page="addresses.lq.page.value" :page-size="addresses.lq.pageSize.value" :sort="addresses.lq.sort.value" caption="Addresses" empty-title="No addresses" @update:page="addresses.lq.setPage" @update:page-size="addresses.lq.setPageSize" @update:sort="addresses.lq.setSort">
        <template #cell-address="{ row }">{{ row.address }} <UiBadge v-if="row.is_primary" color="primary" size="xs">primary</UiBadge> <UiBadge v-if="row.report_state === 'not_reported'" color="neutral" size="xs">not reported</UiBadge> <UiBadge v-if="row.conflict" color="error" size="xs">conflict</UiBadge> <UiBadge v-if="row.previous_device_id" color="info" size="xs">moved</UiBadge></template>
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', dhcp: 'accent', deprecated: 'warning', offline: 'neutral' }" /></template>
      </UiDataTable>
    </UiCard>
    <UiCard v-if="tab === 'guests'" :padded="false" data-test="device-guests">
      <UiDataTable :items="guestRows" :columns="guestColumns" :loading="guests.loading.value" :total="guests.total.value" :page="guests.lq.page.value" :page-size="guests.lq.pageSize.value" :sort="guests.lq.sort.value" caption="Guests" empty-title="No guests" @update:page="guests.lq.setPage" @update:page-size="guests.lq.setPageSize" @update:sort="guests.lq.setSort">
        <template #cell-name="{ row }">
          <RouterLink v-if="row.guest_device_id" class="link" :to="{ name: 'ipam-device', params: { id: row.guest_device_id } }">{{ row.guest_device_name || row.name || row.guest_ref }}</RouterLink>
          <span v-else>{{ row.name || row.guest_ref }} <UiBadge color="neutral" size="xs">not in IPAM</UiBadge></span>
        </template>
      </UiDataTable>
    </UiCard>
    <HardwarePanel v-if="tab === 'hardware' && showHardware" :device-id="id" :has-hardware="!!device?.hardware_summary" />
    <IpmiKvm v-if="tab === 'oob' && canOob" :device-id="id" />
    <UiRecordDrawer v-model="editing" close-on-save :title="'Edit ' + (device?.name ?? 'device')" :schema="deviceSchema" :fields="fields" :initial="device ? { ...device } : undefined" :submit="saveDevice" size="lg" />
  </UiPage>
</template>
