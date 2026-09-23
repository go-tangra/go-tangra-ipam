<script setup lang="ts">
import { onMounted } from 'vue'
import { UiPage, UiAlert, UiCard, UiForm, UiInput, UiSelect, UiButton, UiDataTable, UiStatusChip, type Column, type SelectOption } from '@freya/ui'
import { useZodForm } from '@freya/ui/forms'
import { useVlans } from '@/stores/vlans'
import { vlanFilterSchema, VLAN_STATUSES } from '@/schemas'
import type { Vlan } from '@/api/types'

const store = useVlans()
const statusOptions: SelectOption[] = VLAN_STATUSES.map((s) => ({ title: s, value: s }))
onMounted(() => void store.list())
const filter = useZodForm(vlanFilterSchema, { initial: { domain: '' }, onSubmit: (f) => store.list({ domain: f.domain || undefined, status: f.status }) })
const reload = () => void filter.submit()
const columns: Column<Vlan>[] = [
  { key: 'vlan_id', label: 'VLAN ID', width: 'sm', sortable: true },
  { key: 'name', label: 'Name', sortable: true },
  { key: 'domain', label: 'Domain', hideOnStack: true },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'subnet_count', label: 'Subnets', align: 'end', format: (v) => String(v.subnet_count ?? 0) },
]
</script>

<template>
  <UiPage title="VLANs">
    <template #actions><UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="reload" /></template>
    <template #filters>
      <UiForm :form="filter" class="w-full">
        <div class="grid grid-cols-1 gap-2 md:grid-cols-12 md:items-end">
          <div class="md:col-span-6"><UiInput v-bind="filter.field('domain')" label="Domain" size="sm" @enter="reload" /></div>
          <div class="md:col-span-6"><UiSelect v-bind="filter.field('status')" label="Status" :options="statusOptions" size="sm" @update:model-value="reload" /></div>
        </div>
      </UiForm>
    </template>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiCard :padded="false">
      <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" caption="VLANs" empty-title="No VLANs match" :row-attrs="(v) => ({ 'data-test': 'vlan-row-' + v.id })" data-test="vlans-table">
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', deprecated: 'warning' }" /></template>
      </UiDataTable>
    </UiCard>
  </UiPage>
</template>
