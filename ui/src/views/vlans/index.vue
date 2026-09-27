<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAbility } from '@casl/vue'
import { UiPage, UiAlert, UiCard, UiForm, UiInput, UiSelect, UiButton, UiDataTable, UiStatusChip, UiRecordDrawer, useConfirm, type Column, type SelectOption } from '@go-tangra/ui'
import { useZodForm, zodToFields } from '@go-tangra/ui/forms'
import { useVlans } from '@/stores/vlans'
import { vlanFilterSchema, vlanSchema, VLAN_STATUSES } from '@/schemas'
import type { Vlan } from '@/api/types'
import { ApiError, describe } from '@/api/client'
import { mergeEdit } from '@/api/merge'

const store = useVlans()
const confirm = useConfirm()
const ability = useAbility()
const canCreate = computed(() => ability.can('create', 'Vlan'))
const canUpdate = computed(() => ability.can('update', 'Vlan'))
const canDelete = computed(() => ability.can('delete', 'Vlan'))
const error = ref('')
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

// --- create / edit ---
const dialog = ref(false)
const editing = ref<Vlan | null>(null)
const vlanKeys = Object.keys(vlanSchema.shape)
const fields = zodToFields(vlanSchema, {
  vlan_id: { label: 'VLAN ID', cols: 4, hint: '1–4094' },
  name: { cols: 8 },
  domain: { cols: 8, hint: 'Optional switching domain, e.g. a site or fabric.' },
  status: { cols: 4 },
})
function add(): void {
  editing.value = null
  dialog.value = true
}
function edit(v: Vlan): void {
  if (!canUpdate.value) return
  editing.value = v
  dialog.value = true
}
const initial = computed(() => (editing.value ? { ...editing.value } : { status: 'active' }))

// VLAN ids and names are unique per tenant (whatever the domain); the server
// answers a clash with a bare 409, so name the clashing field from the list.
function conflictError(v: Record<string, unknown>): ApiError {
  const others = store.items.filter((x) => x.id !== editing.value?.id)
  const byId = others.find((x) => x.vlan_id === v.vlan_id)
  const byName = others.find((x) => x.name === v.name)
  const fields: Record<string, string> = {}
  if (byId) fields.vlan_id = `VLAN ${byId.vlan_id} already exists (${byId.name}${byId.domain ? ', ' + byId.domain : ''}).`
  if (byName) fields.name = `The name is already used by VLAN ${byName.vlan_id}.`
  if (!byId && !byName) fields.vlan_id = 'A VLAN with this ID or name already exists.'
  return new ApiError(409, 'validation_failed', { fields })
}
async function submit(v: Record<string, unknown>): Promise<Vlan> {
  try {
    return editing.value ? await store.update(editing.value.id, mergeEdit(editing.value, v, vlanKeys)) : await store.create(v)
  } catch (e) {
    throw e instanceof ApiError && e.reason === 'conflict' ? conflictError(v) : e
  }
}

// --- delete ---
async function remove(v: Vlan): Promise<void> {
  const bound = v.subnet_count ?? 0
  const text = bound ? `${bound} subnet${bound === 1 ? ' is' : 's are'} bound to it; they are kept without a VLAN.` : undefined
  if (!(await confirm.ask({ title: `Delete VLAN ${v.vlan_id} (${v.name})?`, ...(text ? { text } : {}), danger: true, confirmLabel: 'Delete' }))) return
  error.value = ''
  try {
    await store.remove(v.id, bound > 0)
    reload()
  } catch (e) {
    error.value = describe(e)
  }
}
</script>

<template>
  <UiPage title="VLANs">
    <template #actions>
      <UiButton v-if="canCreate" icon="mdi-plus" data-test="vlan-new" @click="add">New VLAN</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="reload" />
    </template>
    <template #filters>
      <UiForm :form="filter" class="w-full">
        <div class="grid grid-cols-1 gap-2 md:grid-cols-12 md:items-end">
          <div class="md:col-span-6"><UiInput v-bind="filter.field('domain')" label="Domain" size="sm" @enter="reload" /></div>
          <div class="md:col-span-6"><UiSelect v-bind="filter.field('status')" label="Status" :options="statusOptions" size="sm" @update:model-value="reload" /></div>
        </div>
      </UiForm>
    </template>
    <UiAlert v-if="error" kind="error" class="mb-3" data-test="vlan-error">{{ error }}</UiAlert>
    <UiAlert v-if="store.error" kind="error" class="mb-3">{{ store.error }}</UiAlert>
    <UiCard :padded="false">
      <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" caption="VLANs" empty-title="No VLANs match" :clickable="canUpdate" :row-attrs="(v) => ({ 'data-test': 'vlan-row-' + v.id })" data-test="vlans-table" @row-click="edit">
        <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', deprecated: 'warning' }" /></template>
        <template v-if="canUpdate || canDelete" #actions="{ row }">
          <UiButton v-if="canUpdate" size="xs" variant="text" icon="mdi-pencil-outline" icon-only label="Edit VLAN" :data-test="'vlan-edit-' + row.id" @click.stop="edit(row)" />
          <UiButton v-if="canDelete" size="xs" variant="text" color="error" icon="mdi-delete-outline" icon-only label="Delete VLAN" :data-test="'vlan-delete-' + row.id" @click.stop="remove(row)" />
        </template>
      </UiDataTable>
    </UiCard>
    <UiRecordDrawer v-model="dialog" close-on-save :title="editing ? `Edit VLAN ${editing.vlan_id}` : 'New VLAN'" :schema="vlanSchema" :fields="fields" :initial="initial" :submit="submit" @saved="reload" />
  </UiPage>
</template>
