<script setup lang="ts">
// Subnet list + tree. Everything done *to* a subnet lives in the right-hand
// drawer (view → scan / split / add child / edit / delete), as in go-tangra.
import { computed, onMounted, ref } from 'vue'
import { UiPage, UiAlert, UiCard, UiForm, UiInput, UiSelect, UiButton, UiBadge, UiDataTable, UiStatusChip, UiTree, type Column, type SelectOption, type TreeNode } from '@freya/ui'
import { useZodForm } from '@freya/ui/forms'
import { useSubnets } from '@/stores/subnets'
import { useVlans } from '@/stores/vlans'
import { useLocations } from '@/stores/locations'
import { subnetFilterSchema, SUBNET_STATUSES } from '@/schemas'
import type { Subnet, SubnetTreeNode } from '@/api/types'
import SubnetDrawer, { type SubnetDrawerMode } from './drawer.vue'

const store = useSubnets()
const vlans = useVlans()
const locations = useLocations()
const statusOptions: SelectOption[] = SUBNET_STATUSES.map((s) => ({ title: s, value: s }))
const versionOptions: SelectOption[] = [{ title: 'IPv4', value: '4' }, { title: 'IPv6', value: '6' }]

onMounted(() => {
  void store.list()
  void store.loadTree()
  void vlans.list()
  void locations.list()
})
const filter = useZodForm(subnetFilterSchema, {
  initial: { q: '' },
  onSubmit: (f) => store.list({ query: f.q || undefined, status: f.status, ip_version: f.ip_version ? Number(f.ip_version) : undefined }),
})
const reload = () => void filter.submit()
async function refreshAll(): Promise<void> {
  await Promise.all([filter.submit(), store.loadTree()])
}

function utilPct(s: Subnet): number {
  if (typeof s.utilization === 'number') return Math.round(s.utilization * (s.utilization <= 1 ? 100 : 1))
  const total = s.total_addresses ?? 0
  return total > 0 ? Math.round(((s.used_addresses ?? 0) / total) * 100) : 0
}
const utilClass = (pct: number) => (pct >= 90 ? 'progress-error' : pct >= 75 ? 'progress-warning' : 'progress-success')
const parentCidr = (id?: string) => store.items.find((s) => s.id === id)?.cidr ?? ''

const toNode = (n: SubnetTreeNode): TreeNode => ({ id: n.id, label: n.cidr, icon: 'mdi-ip-network-outline', badge: n.name, children: (n.children ?? []).map(toNode) })
const tree = computed<TreeNode[]>(() => store.tree.map(toNode))

// --- drawer state ---
const drawer = ref<{ id: string | null; mode: SubnetDrawerMode; parentId?: string | undefined }>({ id: null, mode: 'view' })
const selectedTree = computed({ get: () => drawer.value.id ?? '', set: () => {} })
const openSubnet = (id: string) => (drawer.value = { id, mode: 'view' })
const navigate = (id: string, mode: SubnetDrawerMode, parentId?: string) => (drawer.value = { id: id || null, mode, parentId })
const closeDrawer = () => (drawer.value = { id: null, mode: 'view' })

const columns: Column<Subnet>[] = [
  { key: 'name', label: 'Name', sortable: true },
  { key: 'cidr', label: 'CIDR', sortable: true },
  { key: 'parent_id', label: 'Parent', hideOnStack: true, format: (s) => parentCidr(s.parent_id) },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'utilization', label: 'Utilization', width: 'lg', format: (s) => `${utilPct(s)}% (${s.used_addresses ?? 0}/${s.total_addresses ?? 0})` },
]
</script>

<template>
  <UiPage title="Subnets">
    <template #actions>
      <UiButton icon="mdi-plus" data-test="subnet-new" @click="navigate('', 'create')">New subnet</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="refreshAll" />
    </template>
    <!-- The tree sits beside the table only when there is room for both; below
         that it stacks on top with a capped, scrollable height. -->
    <div class="grid grid-cols-1 gap-4 2xl:grid-cols-12">
      <UiCard title="Tree" class="2xl:col-span-3">
        <div class="max-h-72 overflow-y-auto 2xl:max-h-[70vh]"><UiTree v-model:selected="selectedTree" :items="tree" @select="openSubnet($event.id)" /></div>
      </UiCard>
      <div class="flex min-w-0 flex-col gap-3 2xl:col-span-9">
        <UiCard>
          <UiForm :form="filter">
            <div class="grid grid-cols-2 gap-2 md:grid-cols-12 md:items-end">
              <div class="col-span-2 md:col-span-6"><UiInput v-bind="filter.field('q')" label="Search (name / CIDR)" type="search" size="sm" @enter="reload" /></div>
              <div class="md:col-span-3"><UiSelect v-bind="filter.field('status')" label="Status" :options="statusOptions" size="sm" @update:model-value="reload" /></div>
              <div class="md:col-span-3"><UiSelect v-bind="filter.field('ip_version')" label="Version" :options="versionOptions" size="sm" @update:model-value="reload" /></div>
            </div>
          </UiForm>
        </UiCard>
        <UiAlert v-if="store.error" kind="error">{{ store.error }}</UiAlert>
        <UiCard :padded="false">
          <UiDataTable :items="store.items" :columns="columns" :loading="store.loading" caption="Subnets — select one to view and act on it" empty-title="No subnets match" clickable :row-attrs="(s) => ({ 'data-test': 'subnet-row-' + s.id })" data-test="subnets-table" @row-click="openSubnet($event.id)">
            <template #cell-cidr="{ row }">{{ row.cidr }} <UiBadge v-if="row.ip_version === 6" size="xs">v6</UiBadge></template>
            <template #cell-status="{ row }"><UiStatusChip :status="row.status" :colors="{ reserved: 'info', deprecated: 'warning', deleted: 'neutral' }" /></template>
            <template #cell-utilization="{ row }">
              <div class="flex items-center gap-2">
                <progress class="progress h-2 w-24" :class="utilClass(utilPct(row))" :value="utilPct(row)" max="100" :aria-label="'Utilization ' + utilPct(row) + '%'" />
                <span class="text-xs whitespace-nowrap">{{ utilPct(row) }}% ({{ row.used_addresses ?? 0 }}/{{ row.total_addresses ?? 0 }})</span>
              </div>
            </template>
          </UiDataTable>
        </UiCard>
      </div>
    </div>

    <SubnetDrawer :subnet-id="drawer.id" :mode="drawer.mode" :parent-id="drawer.parentId" @close="closeDrawer" @changed="refreshAll" @navigate="navigate" />
  </UiPage>
</template>
