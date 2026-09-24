<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { UiPage, UiAlert, UiCard, UiButton, UiDataTable, UiStatusChip, UiLiveIndicator, UiDrawer, UiForm, UiSelect, UiSwitch, useConfirm, type Column, type SelectOption } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { useScans } from '@/stores/scans'
import { useSubnets } from '@/stores/subnets'
import { useLive } from '@/stores/live'
import { startScanSchema } from '@/schemas'
import type { IPScanJob } from '@/api/types'
import { describe } from '@/api/client'

const store = useScans()
const subnets = useSubnets()
const live = useLive()
const confirm = useConfirm()
const statusColors = { pending: 'neutral', scanning: 'info', completed: 'success', failed: 'error', cancelled: 'neutral' } as const
const progressClass: Record<string, string> = { pending: 'progress-neutral', scanning: 'progress-info', completed: 'progress-success', failed: 'progress-error', cancelled: 'progress-neutral' }

let release: (() => void) | null = null
onMounted(() => {
  void store.list()
  void subnets.list()
  release = live.connect()
})
onUnmounted(() => release?.())
const subnetOptions = computed<SelectOption[]>(() => subnets.items.map((s) => ({ title: s.name + ' (' + s.cidr + ')', value: s.id })))

const startOpen = ref(false)
const actionError = ref('')
const start = useZodForm(startScanSchema, {
  onSubmit: (v) => store.start({ subnet_id: v.subnet_id, enable_snmp: v.enable_snmp, enable_dns_update: v.enable_dns_update }),
  onSuccess: () => (startOpen.value = false),
})
function openStart(): void {
  start.reset({ subnet_id: subnets.items[0]?.id ?? '', enable_snmp: false, enable_dns_update: false })
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
const subnetLabel = (id: string): string => subnets.items.find((x) => x.id === id)?.cidr ?? id
const columns: Column<IPScanJob>[] = [
  { key: 'subnet_id', label: 'Subnet', format: (j) => subnetLabel(j.subnet_id) },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'progress', label: 'Progress', width: 'lg', format: (j) => j.progress + '%' },
  { key: 'alive_count', label: 'Alive', align: 'end', format: (j) => String(j.alive_count ?? 0) },
  { key: 'new_count', label: 'New', align: 'end', format: (j) => String(j.new_count ?? 0), hideOnStack: true },
]
</script>

<template>
  <UiPage title="Discovery scans">
    <template #badges><UiLiveIndicator :connected="live.connected" /></template>
    <template #actions><UiButton size="sm" icon="mdi-radar" @click="openStart">Start scan</UiButton></template>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiAlert v-if="actionError" kind="error" class="mb-3">{{ actionError }}</UiAlert>
    <UiCard :padded="false">
      <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" caption="Scans" empty-title="No scans yet" :row-attrs="(j) => ({ 'data-test': 'scan-row-' + j.id })" data-test="scans-table">
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="statusColors" /></template>
        <template #cell-progress="{ row }">
          <div class="flex items-center gap-2">
            <progress class="progress h-2 w-28" :class="progressClass[row.status]" :value="row.status === 'scanning' && !row.progress ? undefined : row.progress" max="100" :aria-label="'Progress ' + row.progress + '%'" />
            <span class="text-xs">{{ row.progress }}%</span>
          </div>
        </template>
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
