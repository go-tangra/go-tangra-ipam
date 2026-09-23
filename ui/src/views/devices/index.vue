<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { UiPage, UiAlert, UiCard, UiForm, UiInput, UiSelect, UiButton, UiDataTable, UiStatusChip, UiBadge, UiRecordDrawer, type Column, type SelectOption } from '@freya/ui'
import { useZodForm } from '@freya/ui/forms'
import { useDevices } from '@/stores/devices'
import { deviceFilterSchema, deviceSchema, DEVICE_TYPES, DEVICE_STATUSES } from '@/schemas'
import type { Device } from '@/api/types'
import { statusColors } from './colors'
import { useDeviceFields } from './fields'

const router = useRouter()
const store = useDevices()
const typeOptions: SelectOption[] = DEVICE_TYPES.map((s) => ({ title: s, value: s }))
const statusOptions: SelectOption[] = DEVICE_STATUSES.map((s) => ({ title: s, value: s }))
onMounted(() => void store.list())
const filter = useZodForm(deviceFilterSchema, { initial: { q: '' }, onSubmit: (f) => store.list({ query: f.q || undefined, device_type: f.device_type, status: f.status }) })
const reload = () => void filter.submit()
const creating = ref(false)
const fields = useDeviceFields()
const createDevice = (v: Record<string, unknown>) => store.create(v)
const columns: Column<Device>[] = [
  { key: 'name', label: 'Name', sortable: true },
  { key: 'device_type', label: 'Type', width: 'sm' },
  { key: 'model', label: 'Model', format: (d) => [d.manufacturer, d.model].filter(Boolean).join(' '), hideOnStack: true },
  { key: 'management_ip', label: 'Management IP', format: (d) => d.management_ip || d.primary_ip || '' },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'updates', label: 'Updates', width: 'sm', format: (d) => [d.security_update_count ? d.security_update_count + ' sec' : '', d.package_update_count ? d.package_update_count + ' pkg' : ''].filter(Boolean).join(' ') },
  { key: 'interface_count', label: 'NICs', align: 'end', format: (d) => String(d.interface_count ?? 0), hideOnStack: true },
]
</script>

<template>
  <UiPage title="Devices">
    <template #actions>
      <UiButton icon="mdi-plus" data-test="device-new" @click="creating = true">New device</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="reload" />
    </template>
    <template #filters>
      <UiForm :form="filter" class="w-full">
        <div class="grid grid-cols-2 gap-2 md:grid-cols-12 md:items-end">
          <div class="col-span-2 md:col-span-6"><UiInput v-bind="filter.field('q')" label="Search (name)" type="search" size="sm" @enter="reload" /></div>
          <div class="md:col-span-3"><UiSelect v-bind="filter.field('device_type')" label="Type" :options="typeOptions" size="sm" @update:model-value="reload" /></div>
          <div class="md:col-span-3"><UiSelect v-bind="filter.field('status')" label="Status" :options="statusOptions" size="sm" @update:model-value="reload" /></div>
        </div>
      </UiForm>
    </template>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiCard :padded="false">
      <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" caption="Devices" empty-title="No devices match" clickable :row-attrs="(d) => ({ 'data-test': 'device-row-' + d.id })" data-test="devices-table" @row-click="router.push({ name: 'ipam-device', params: { id: $event.id } })">
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="statusColors" /></template>
        <template #cell-updates="{ row }">
          <UiBadge v-if="(row.security_update_count ?? 0) > 0" color="error" size="xs">{{ row.security_update_count }} sec</UiBadge>
          <UiBadge v-if="(row.package_update_count ?? 0) > 0" color="warning" size="xs">{{ row.package_update_count }} pkg</UiBadge>
        </template>
      </UiDataTable>
    </UiCard>
    <UiRecordDrawer v-model="creating" close-on-save title="New device" :schema="deviceSchema" :fields="fields" :initial="{ device_type: 'server', status: 'active', device_height_u: 1 }" :submit="createDevice" size="lg" @saved="router.push({ name: 'ipam-device', params: { id: ($event as Device).id } })" />
  </UiPage>
</template>
