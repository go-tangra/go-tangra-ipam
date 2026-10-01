<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { UiPage, UiAlert, UiCard, UiButton, UiDataTable, UiStatusChip, UiLiveIndicator, UiDrawer, UiForm, UiSelect, UiSwitch, useConfirm, useListQuery, type Column, type SelectOption } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { SCAN_LIST, useScans } from '@/stores/scans'
import { useSubnets } from '@/stores/subnets'
import { useLive } from '@/stores/live'
import { startScanSchema } from '@/schemas'
import type { IPScanJob } from '@/api/types'
import { describe } from '@/api/client'
import { arpPhaseText, snmpPhaseText } from './snmp'

const store = useScans()
const subnets = useSubnets()
const live = useLive()
const confirm = useConfirm()
const statusColors = { pending: 'neutral', scanning: 'info', completed: 'success', failed: 'error', cancelled: 'neutral' } as const
const progressClass: Record<string, string> = { pending: 'progress-neutral', scanning: 'progress-info', completed: 'progress-success', failed: 'progress-error', cancelled: 'progress-neutral' }

// --- server paging and sorting (page / size / sort in the URL: ?scans.page=…).
// Live scan events reload the shown page (stores/live.ts), never prepend.
const lq = useListQuery('scans', SCAN_LIST)
async function load(): Promise<void> {
  const res = await store.list({}, lq.query.value)
  if (res?.page) lq.clampTo(res.page) // a page beyond the end answers the last page
}
watch(lq.query, () => void load())

let release: (() => void) | null = null
onMounted(() => {
  void load()
  void subnets.loadAll()
  release = live.connect()
})
onUnmounted(() => release?.())
const subnetOptions = computed<SelectOption[]>(() => subnets.all.map((s) => ({ title: s.name + ' (' + s.cidr + ')', value: s.id })))

const startOpen = ref(false)
const actionError = ref('')
const start = useZodForm(startScanSchema, {
  onSubmit: (v) => store.start({ subnet_id: v.subnet_id, enable_snmp: v.enable_snmp, enable_dns_update: v.enable_dns_update }),
  onSuccess: () => (startOpen.value = false),
})
function openStart(): void {
  start.reset({ subnet_id: subnets.all[0]?.id ?? '', enable_snmp: false, enable_dns_update: false })
  startOpen.value = true
}
async function cancel(job: IPScanJob): Promise<void> {
  if (!(await confirm.ask({ title: 'Cancel this scan?', confirmLabel: 'Cancel scan' }))) return
  actionError.value = ''
  try {
    await store.cancel(job.id)
  } catch (e) {
    actionError.value = describe(e)
  }
}
const subnetLabel = (id: string): string => subnets.all.find((x) => x.id === id)?.cidr ?? id
// Created, subnet and status are server sort fields (SCAN_LIST; subnet orders
// by the subnet id, which groups a subnet's scans).
const columns: Column<IPScanJob>[] = [
  { key: 'created_at', label: 'Created', sortable: true, defaultDir: 'desc', hideOnStack: true, format: (j) => (j.created_at ? new Date(j.created_at).toLocaleString() : '') },
  { key: 'subnet', label: 'Subnet', sortable: true, format: (j) => subnetLabel(j.subnet_id) },
  { key: 'status', label: 'Status', width: 'sm', sortable: true },
  { key: 'progress', label: 'Progress', width: 'lg', format: (j) => j.progress + '%' },
  { key: 'alive_count', label: 'Alive', align: 'end', format: (j) => String(j.alive_count ?? 0) },
  { key: 'new_count', label: 'New', align: 'end', format: (j) => String(j.new_count ?? 0), hideOnStack: true },
  { key: 'snmp_status', label: 'SNMP', format: (j) => snmpPhaseText(j, subnetLabel), hideOnStack: true },
  { key: 'arp_status', label: 'ARP', format: (j) => arpPhaseText(j), hideOnStack: true },
]
</script>

<template>
  <UiPage title="Discovery scans">
    <template #badges><UiLiveIndicator :connected="live.connected" /></template>
    <template #actions><UiButton size="sm" icon="mdi-radar" @click="openStart">Start scan</UiButton></template>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiAlert v-if="actionError" kind="error" class="mb-3">{{ actionError }}</UiAlert>
    <UiCard :padded="false">
      <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" :total="store.total" :page="lq.page.value" :page-size="lq.pageSize.value" :sort="lq.sort.value" caption="Scans" empty-title="No scans yet" :row-attrs="(j) => ({ 'data-test': 'scan-row-' + j.id })" data-test="scans-table" @update:page="lq.setPage" @update:page-size="lq.setPageSize" @update:sort="lq.setSort">
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="statusColors" /></template>
        <template #cell-progress="{ row }">
          <div class="flex items-center gap-2">
            <progress class="progress h-2 w-28" :class="progressClass[row.status]" :value="row.status === 'scanning' && !row.progress ? undefined : row.progress" max="100" :aria-label="'Progress ' + row.progress + '%'" />
            <span class="text-xs">{{ row.progress }}%</span>
          </div>
        </template>
        <template #cell-snmp_status="{ row }"><span class="text-xs" :class="row.snmp_status && row.snmp_status !== 'ran' && row.snmp_status !== 'not_requested' ? 'text-warning' : ''" data-test="snmp-phase">{{ snmpPhaseText(row, subnetLabel) }}</span></template>
        <template #cell-arp_status="{ row }"><span class="text-xs" :class="row.arp_status === 'failed' ? 'text-warning' : ''" data-test="arp-phase">{{ arpPhaseText(row) }}</span></template>
        <template #actions="{ row }">
          <UiButton v-if="row.status === 'pending' || row.status === 'scanning'" size="xs" variant="text" color="error" icon="mdi-cancel" @click="cancel(row)">Cancel</UiButton>
        </template>
      </UiDataTable>
    </UiCard>
    <UiDrawer v-model="startOpen" title="Start discovery scan" size="md">
      <UiForm :form="start">
        <div class="flex flex-col gap-3">
          <UiSelect v-bind="start.field('subnet_id')" label="Subnet" :options="subnetOptions" :clearable="false" required />
          <UiSwitch v-bind="start.field('enable_snmp')" label="SNMP discovery" />
          <UiSwitch v-bind="start.field('enable_dns_update')" label="Update DNS" />
        </div>
      </UiForm>
      <template #actions>
        <UiButton variant="text" @click="startOpen = false">Cancel</UiButton>
        <UiButton :loading="start.submitting.value" @click="start.submit()">Start</UiButton>
      </template>
    </UiDrawer>
  </UiPage>
</template>
