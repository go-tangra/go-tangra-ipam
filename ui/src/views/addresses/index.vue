<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { UiPage, UiAlert, UiCard, UiForm, UiInput, UiSelect, UiNumberInput, UiButton, UiDataTable, UiStatusChip, UiBadge, UiLiveIndicator, UiDrawer, UiTooltip, useConfirm, type Column, type SelectOption } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { useAddresses } from '@/stores/addresses'
import { useSubnets } from '@/stores/subnets'
import { useLive } from '@/stores/live'
import { addressFilterSchema, allocateSchema, bulkAllocateSchema, suggestSchema, ADDRESS_STATUSES, ADDRESS_TYPES } from '@/schemas'
import { useHostSync } from '@/stores/hostsync'
import { useDevices } from '@/stores/devices'
import { addressLinkText, addressLinkTitle, macSourceLabel, macSourceText } from './mac'
import { useAbility } from '@casl/vue'
import type { IPAddress, PingResult } from '@/api/types'
import { describe } from '@/api/client'

const store = useAddresses()
const subnets = useSubnets()
const live = useLive()
const confirm = useConfirm()
const hostSync = useHostSync()
const ability = useAbility()
const canClear = computed(() => ability.can('clear', 'AddressConflict'))
const reportOptions: SelectOption[] = [{ title: 'reported', value: 'reported' }, { title: 'no longer reported', value: 'not_reported' }, { title: 'in conflict', value: 'conflict' }]
const reportFilter = ref<string | undefined>(undefined)
const statusOptions: SelectOption[] = ADDRESS_STATUSES.map((s) => ({ title: s, value: s }))
const typeOptions: SelectOption[] = ADDRESS_TYPES.map((s) => ({ title: s, value: s }))
const subnetOptions = computed<SelectOption[]>(() => subnets.items.map((s) => ({ title: s.name + ' (' + s.cidr + ')', value: s.id })))

let release: (() => void) | null = null
// Names of the devices that reported ARP-learned MACs (feature 022).
const devices = useDevices()
const deviceNames = ref<Record<string, string>>({})
const deviceName = (id: string): string => deviceNames.value[id] ?? ''
const macColors = { ARP: 'info', Agent: 'success', Manual: 'neutral' } as const
async function loadDeviceNames(): Promise<void> {
  try {
    deviceNames.value = Object.fromEntries((await devices.lookup()).map((d) => [d.id, d.name]))
  } catch {
    // names are a convenience: the tooltip falls back to the id
  }
}
onMounted(() => {
  void store.list()
  void subnets.list()
  void loadDeviceNames()
  release = live.connect()
})
onUnmounted(() => release?.())

const filter = useZodForm(addressFilterSchema, {
  initial: { hostname: '', mac: '' },
  onSubmit: (f) => store.list({
    subnet_id: f.subnet_id || undefined, status: f.status, address_type: f.address_type, hostname: f.hostname || undefined, mac: f.mac || undefined,
    report_state: reportFilter.value === 'conflict' ? undefined : reportFilter.value, conflict: reportFilter.value === 'conflict' ? true : undefined,
  }),
})
const reload = () => void filter.submit()

// --- allocate / bulk / suggest dialogs: one schema each ---
type Mode = 'single' | 'bulk' | 'suggest'
const mode = ref<Mode | null>(null)
const suggested = ref<string[]>([])
const actionError = ref('')
const single = useZodForm(allocateSchema, { onSubmit: (v) => store.allocate({ subnet_id: v.subnet_id, hostname: v.hostname }), onSuccess: () => (mode.value = null) })
const bulk = useZodForm(bulkAllocateSchema, { onSubmit: (v) => store.bulkAllocate({ subnet_id: v.subnet_id, count: v.count, hostname_prefix: v.hostname_prefix }), onSuccess: () => (mode.value = null) })
const suggest = useZodForm(suggestSchema, { onSubmit: async (v) => { suggested.value = await store.suggest(v.subnet_id, v.count) } })
const forms = { single, bulk, suggest }
const current = computed(() => (mode.value ? forms[mode.value] : null))
function open(m: Mode): void {
  const subnet_id = (filter.values.subnet_id as string | undefined) || subnets.items[0]?.id || ''
  single.reset({ subnet_id, hostname: '' })
  bulk.reset({ subnet_id, count: 1, hostname_prefix: '' })
  suggest.reset({ subnet_id, count: 5 })
  suggested.value = []
  mode.value = m
}
const titles: Record<Mode, string> = { single: 'Allocate next-free', bulk: 'Bulk allocate', suggest: 'Suggest free addresses' }

// --- ping ---
const pingResult = ref<Record<string, PingResult>>({})
const pinging = ref<Record<string, boolean>>({})
async function ping(a: IPAddress): Promise<void> {
  actionError.value = ''
  pinging.value = { ...pinging.value, [a.id]: true }
  try {
    pingResult.value = { ...pingResult.value, [a.id]: await store.ping(a.id) }
  } catch (e) {
    actionError.value = `Ping ${a.address}: ${describe(e)}`
  } finally {
    pinging.value = { ...pinging.value, [a.id]: false }
  }
}
// The button itself reports the last result, so feedback lands where the click was.
function pingLabel(id: string): string {
  const p = pingResult.value[id]
  return !p ? 'Ping' : p.available === false ? 'Unavailable' : p.alive ? `${p.rtt_ms ?? 0} ms` : 'No reply'
}
async function clearConflict(a: IPAddress): Promise<void> {
  actionError.value = ''
  try {
    const updated = await hostSync.clearConflict(a.id)
    store.items = store.items.map((x) => (x.id === a.id ? updated : x))
  } catch (e) {
    actionError.value = describe(e)
  }
}
async function removeAddr(a: IPAddress): Promise<void> {
  if (!(await confirm.ask({ title: `Release ${a.address}?`, danger: true, confirmLabel: 'Release' }))) return
  actionError.value = ''
  try {
    await store.remove(a.id)
  } catch (e) {
    actionError.value = describe(e)
  }
}
const columns: Column<IPAddress>[] = [
  { key: 'address', label: 'Address', sortable: true },
  { key: 'hostname', label: 'Hostname' },
  { key: 'mac_address', label: 'MAC', hideOnStack: true },
  { key: 'link', label: 'Connected to', hideOnStack: true, format: addressLinkText },
  { key: 'address_type', label: 'Type', hideOnStack: true },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'ping', label: 'Ping', width: 'sm', format: (a) => { const p = pingResult.value[a.id]; return p ? (p.alive ? (p.rtt_ms ?? 0) + ' ms' : 'down') : '' } },
]
</script>

<template>
  <UiPage title="IP Addresses">
    <template #badges><UiLiveIndicator :connected="live.connected" /></template>
    <template #actions>
      <UiButton size="sm" variant="soft" icon="mdi-plus" @click="open('single')">Allocate</UiButton>
      <UiButton size="sm" variant="soft" icon="mdi-plus-box-multiple" @click="open('bulk')">Bulk</UiButton>
      <UiButton size="sm" variant="soft" icon="mdi-lightbulb-on-outline" @click="open('suggest')">Suggest</UiButton>
    </template>
    <template #filters>
      <UiForm :form="filter" class="w-full">
        <div class="grid grid-cols-2 gap-2 md:grid-cols-12 md:items-end">
          <div class="col-span-2 md:col-span-2"><UiSelect v-bind="filter.field('subnet_id')" label="Subnet" :options="subnetOptions" size="sm" @update:model-value="reload" /></div>
          <div class="md:col-span-2"><UiSelect v-bind="filter.field('status')" label="Status" :options="statusOptions" size="sm" @update:model-value="reload" /></div>
          <div class="md:col-span-2"><UiSelect v-bind="filter.field('address_type')" label="Type" :options="typeOptions" size="sm" @update:model-value="reload" /></div>
          <div class="md:col-span-2"><UiSelect id="address-report-filter" v-model="reportFilter" label="Host report" :options="reportOptions" size="sm" data-test="address-report-filter" @update:model-value="reload" /></div>
          <div class="md:col-span-2"><UiInput v-bind="filter.field('hostname')" label="Hostname" size="sm" @enter="reload" /></div>
          <div class="md:col-span-2"><UiInput v-bind="filter.field('mac')" label="MAC (full or partial)" size="sm" placeholder="0a5c or d2-f1" @enter="reload" /></div>
        </div>
      </UiForm>
    </template>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiAlert v-if="actionError" kind="error" class="mb-3">{{ actionError }}</UiAlert>
    <UiCard :padded="false">
      <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" caption="IP addresses" empty-title="No addresses match" :row-attrs="(a) => ({ 'data-test': 'address-row-' + a.id })" data-test="addresses-table">
        <template #cell-address="{ row }">{{ row.address }} <UiBadge v-if="row.is_primary" color="primary" size="xs">primary</UiBadge> <UiBadge v-if="row.report_state === 'not_reported'" color="neutral" size="xs">not reported</UiBadge> <UiBadge v-if="row.conflict" color="error" size="xs">conflict</UiBadge> <UiBadge v-if="row.origin === 'arp'" color="info" size="xs">from ARP</UiBadge></template>
        <template #cell-mac_address="{ row }">
          <span v-if="row.mac_address" class="inline-flex items-center gap-1">
            <span class="font-mono text-xs">{{ row.mac_address }}</span>
            <UiTooltip v-if="macSourceLabel(row)" :text="macSourceText(row, deviceName)"><UiBadge :color="macColors[macSourceLabel(row) as keyof typeof macColors]" size="xs" :data-test="'mac-source-' + row.id">{{ macSourceLabel(row) }}</UiBadge></UiTooltip>
            <UiTooltip v-if="row.mac_conflict" :text="'ARP reports ' + row.mac_conflict"><UiBadge color="warning" size="xs" :data-test="'mac-conflict-' + row.id">MAC conflict</UiBadge></UiTooltip>
          </span>
          <span v-else class="text-base-content/70">—</span>
        </template>
        <template #cell-link="{ row }"><UiTooltip v-if="row.link || row.links?.length" :text="addressLinkTitle(row)"><span class="text-xs" :data-test="'address-link-' + row.id">{{ addressLinkText(row) }}</span></UiTooltip><span v-else class="text-base-content/70">—</span></template>
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', dhcp: 'accent', deprecated: 'warning', offline: 'neutral' }" /></template>
        <template #cell-ping="{ row }"><UiStatusChip v-if="pingResult[row.id]" :status="pingResult[row.id]!.alive ? 'alive' : 'down'" :label="pingResult[row.id]!.alive ? (pingResult[row.id]!.rtt_ms ?? 0) + ' ms' : 'down'" :colors="{ alive: 'success', down: 'neutral' }" /><span v-else class="text-base-content/70">—</span></template>
        <template #actions="{ row }">
          <UiButton size="xs" variant="text" :color="pingResult[row.id] ? (pingResult[row.id]!.alive ? 'success' : 'error') : 'primary'" icon="mdi-lan-pending" :loading="pinging[row.id] ?? false" :data-test="'address-ping-' + row.id" @click="ping(row)">{{ pingLabel(row.id) }}</UiButton>
          <UiButton v-if="row.conflict && canClear" size="xs" variant="text" icon="mdi-check-circle-outline" :data-test="'address-clear-' + row.id" @click="clearConflict(row)">Clear conflict</UiButton>
          <UiButton size="xs" variant="text" color="error" icon="mdi-delete-outline" icon-only label="Release" @click="removeAddr(row)" />
        </template>
      </UiDataTable>
    </UiCard>

    <UiDrawer :model-value="mode !== null" :title="mode ? titles[mode] : ''" size="md" @update:model-value="mode = null">
      <UiForm v-if="current && mode" :form="current">
        <div class="flex flex-col gap-3">
          <UiSelect v-bind="current.field('subnet_id')" label="Subnet" :options="subnetOptions" :clearable="false" required />
          <UiInput v-if="mode === 'single'" v-bind="single.field('hostname')" label="Hostname (optional)" />
          <template v-if="mode === 'bulk'">
            <UiNumberInput v-bind="bulk.field('count')" label="Count" :min="1" :max="1024" required />
            <UiInput v-bind="bulk.field('hostname_prefix')" label="Hostname prefix (optional)" />
          </template>
          <template v-if="mode === 'suggest'">
            <UiNumberInput v-bind="suggest.field('count')" label="Count" :min="1" :max="1024" required />
            <div v-if="suggested.length" class="flex flex-wrap gap-1"><UiBadge v-for="ip in suggested" :key="ip" size="md">{{ ip }}</UiBadge></div>
          </template>
        </div>
      </UiForm>
      <template #actions>
        <UiButton variant="text" @click="mode = null">Close</UiButton>
        <UiButton :loading="current?.submitting.value ?? false" @click="current?.submit()">{{ mode === 'suggest' ? 'Suggest' : 'Allocate' }}</UiButton>
      </template>
    </UiDrawer>
  </UiPage>
</template>
