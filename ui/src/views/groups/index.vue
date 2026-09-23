<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { UiPage, UiAlert, UiCard, UiForm, UiInput, UiButton, UiBadge, UiTabs, UiAccordion, UiDataTable, UiStatusChip, UiEmptyState, UiToolbar, UiRecordDrawer, useConfirm, type AccordionItem, type Column, type TabItem } from '@freya/ui'
import { useZodForm, zodToFields } from '@freya/ui/forms'
import { useGroups } from '@/stores/groups'
import { useHostGroups } from '@/stores/hostGroups'
import { useDevices } from '@/stores/devices'
import { checkIpSchema, groupSchema, hostGroupMemberSchema, ipGroupMemberSchema } from '@/schemas'
import type { GroupMatch, HostGroup, HostGroupMember, IPGroup, IPGroupMember } from '@/api/types'
import { describe } from '@/api/client'
import { mergeEdit } from '@/api/merge'

type Kind = 'ip' | 'host'
const groups = useGroups()
const hostGroups = useHostGroups()
const devices = useDevices()
const confirm = useConfirm()
const tab = ref<Kind>('ip')
const error = ref('')
onMounted(() => {
  void groups.list()
  void hostGroups.list()
  void devices.list()
})
const tabs = computed<TabItem[]>(() => [{ key: 'ip', label: 'IP groups', count: groups.items.length }, { key: 'host', label: 'Host groups', count: hostGroups.items.length }])
const storeFor = (k: Kind) => (k === 'ip' ? groups : hostGroups)

// --- check ip ---
const matches = ref<GroupMatch[] | null>(null)
const check = useZodForm(checkIpSchema, {
  initial: { ip: '' },
  onSubmit: async (v) => {
    matches.value = null
    matches.value = await groups.checkIp(v.ip)
  },
})

// --- members (loaded on first open, reloaded after each change) ---
const ipMembers = ref<Record<string, IPGroupMember[]>>({})
const hostMembers = ref<Record<string, HostGroupMember[]>>({})
async function loadIp(id: string): Promise<void> {
  ipMembers.value = { ...ipMembers.value, [id]: await groups.members(id) }
}
async function loadHost(id: string): Promise<void> {
  hostMembers.value = { ...hostMembers.value, [id]: await hostGroups.members(id) }
}
async function toggleIp(id: string, open: boolean): Promise<void> {
  if (open && !ipMembers.value[id]) await loadIp(id)
}
async function toggleHost(id: string, open: boolean): Promise<void> {
  if (open && !hostMembers.value[id]) await loadHost(id)
}
const summary = (g: IPGroup | HostGroup) => `${g.status} · ${g.member_count ?? 0} members${g.description ? ' · ' + g.description : ''}`
const ipItems = computed<AccordionItem[]>(() => groups.items.map((g) => ({ key: g.id, title: g.name, subtitle: summary(g) })))
const hostItems = computed<AccordionItem[]>(() => hostGroups.items.map((g) => ({ key: g.id, title: g.name, subtitle: summary(g) })))
type Row<T> = T & Record<string, unknown>
const ipRows = (id: string) => (ipMembers.value[id] ?? []) as Row<IPGroupMember>[]
const hostRows = (id: string) => (hostMembers.value[id] ?? []) as Row<HostGroupMember>[]
const ipCols: Column<Row<IPGroupMember>>[] = [{ key: 'sequence', label: '#', width: 'sm' }, { key: 'member_type', label: 'Type', width: 'sm' }, { key: 'value', label: 'Value' }, { key: 'description', label: 'Description', hideOnStack: true }]
const hostCols: Column<Row<HostGroupMember>>[] = [{ key: 'sequence', label: '#', width: 'sm' }, { key: 'device_name', label: 'Device', format: (m) => m.device_name || m.device_id }, { key: 'device_type', label: 'Type', hideOnStack: true }, { key: 'device_status', label: 'Status', width: 'sm' }, { key: 'device_primary_ip', label: 'Primary IP' }]
const nextSeq = (rows: { sequence?: number }[]) => rows.reduce((m, r) => Math.max(m, r.sequence ?? 0), 0) + 1

// --- group create / edit / delete ---
const groupDialog = ref(false)
const groupKind = ref<Kind>('ip')
const editingGroup = ref<IPGroup | HostGroup | null>(null)
const groupKeys = Object.keys(groupSchema.shape)
const groupFields = zodToFields(groupSchema)
function newGroup(): void {
  groupKind.value = tab.value
  editingGroup.value = null
  groupDialog.value = true
}
function editGroup(kind: Kind, g: IPGroup | HostGroup): void {
  groupKind.value = kind
  editingGroup.value = g
  groupDialog.value = true
}
const groupInitial = computed(() => (editingGroup.value ? { ...editingGroup.value } : { status: 'active' }))
function submitGroup(v: Record<string, unknown>): Promise<unknown> {
  const s = storeFor(groupKind.value)
  return editingGroup.value ? s.update(editingGroup.value.id, mergeEdit(editingGroup.value, v, groupKeys)) : s.create(v)
}
async function removeGroup(kind: Kind, g: IPGroup | HostGroup): Promise<void> {
  if (!(await confirm.ask({ title: `Delete ${g.name}?`, text: 'Its members are removed with it.', danger: true, confirmLabel: 'Delete' }))) return
  error.value = ''
  try {
    await storeFor(kind).remove(g.id)
  } catch (e) {
    error.value = describe(e)
  }
}

// --- member add / edit / remove ---
const memberDialog = ref(false)
const memberKind = ref<Kind>('ip')
const memberGroup = ref('')
const editingMember = ref<IPGroupMember | HostGroupMember | null>(null)
const ipMemberFields = zodToFields(ipGroupMemberSchema, {
  member_type: { label: 'Type', cols: 4 },
  value: { cols: 8, hint: 'Address 10.0.0.5 · range 10.0.0.10-10.0.0.20 · subnet 10.0.0.0/24' },
  sequence: { label: 'Order', cols: 4 },
  description: { cols: 12, type: 'text' },
})
const hostMemberFields = computed(() =>
  zodToFields(hostGroupMemberSchema, {
    device_id: { label: 'Device', type: 'select', cols: 8, options: devices.items.map((d) => ({ title: `${d.name} (${d.device_type})`, value: d.id })) },
    sequence: { label: 'Order', cols: 4 },
  }),
)
async function addMember(kind: Kind, groupId: string): Promise<void> {
  // The next order number comes from the member list, so load it if the
  // group was never expanded.
  if (kind === 'ip' && !ipMembers.value[groupId]) await loadIp(groupId)
  if (kind === 'host' && !hostMembers.value[groupId]) await loadHost(groupId)
  memberKind.value = kind
  memberGroup.value = groupId
  editingMember.value = null
  memberDialog.value = true
}
function editMember(kind: Kind, groupId: string, m: IPGroupMember | HostGroupMember): void {
  memberKind.value = kind
  memberGroup.value = groupId
  editingMember.value = m
  memberDialog.value = true
}
const memberInitial = computed(() => {
  if (editingMember.value) return { ...editingMember.value }
  const seq = nextSeq(memberKind.value === 'ip' ? ipRows(memberGroup.value) : hostRows(memberGroup.value))
  return memberKind.value === 'ip' ? { member_type: 'address', sequence: seq } : { sequence: seq }
})
async function submitMember(v: Record<string, unknown>): Promise<unknown> {
  const gid = memberGroup.value
  const m = editingMember.value
  if (memberKind.value === 'ip') {
    const r = m ? await groups.updateMember(gid, m.id, v) : await groups.addMember(gid, v)
    await Promise.all([loadIp(gid), groups.list()])
    return r
  }
  const r = m ? await hostGroups.updateMember(gid, m.id, v) : await hostGroups.addMember(gid, v)
  await Promise.all([loadHost(gid), hostGroups.list()])
  return r
}
async function removeMember(kind: Kind, groupId: string, m: IPGroupMember | HostGroupMember): Promise<void> {
  const label = 'value' in m ? m.value : m.device_name || m.device_id
  if (!(await confirm.ask({ title: `Remove ${label}?`, danger: true, confirmLabel: 'Remove' }))) return
  error.value = ''
  try {
    if (kind === 'ip') {
      await groups.removeMember(groupId, m.id)
      await Promise.all([loadIp(groupId), groups.list()])
    } else {
      await hostGroups.removeMember(groupId, m.id)
      await Promise.all([loadHost(groupId), hostGroups.list()])
    }
  } catch (e) {
    error.value = describe(e)
  }
}
</script>

<template>
  <UiPage title="Groups">
    <template #actions><UiButton icon="mdi-plus" data-test="group-new" @click="newGroup">{{ tab === 'ip' ? 'New IP group' : 'New host group' }}</UiButton></template>
    <UiCard class="mb-4">
      <UiForm :form="check">
        <div class="flex flex-wrap items-end gap-2">
          <UiInput v-bind="check.field('ip')" label="Check IP membership" placeholder="10.0.0.5" class="w-full md:w-80" @enter="check.submit()" />
          <UiButton type="submit" variant="soft" icon="mdi-magnify" :loading="check.submitting.value">Check</UiButton>
        </div>
      </UiForm>
      <div v-if="matches" class="mt-3">
        <div v-if="matches.length" class="flex flex-wrap gap-1"><UiBadge v-for="m in matches" :key="m.group_id" color="primary" size="md">{{ m.name ?? m.group_id }}<span v-if="m.value" class="ms-1 text-xs">({{ m.value }})</span></UiBadge></div>
        <span v-else class="text-sm text-base-content/70">No groups contain that address.</span>
      </div>
    </UiCard>
    <UiAlert v-if="error" kind="error" class="mb-3">{{ error }}</UiAlert>
    <UiTabs v-model="tab" :tabs="tabs" class="mb-3" />
    <template v-if="tab === 'ip'">
      <UiAlert v-if="groups.error" kind="error" class="mb-3">{{ groups.error }}</UiAlert>
      <UiEmptyState v-if="!groups.items.length" title="No IP groups" text="Group addresses, ranges and subnets to reuse them in policies." />
      <UiAccordion v-else :items="ipItems" @toggle="toggleIp">
        <template v-for="g in groups.items" :key="g.id" #[g.id]>
          <UiToolbar class="mb-2">
            <UiStatusChip :status="g.status" :colors="{ inactive: 'neutral' }" />
            <span class="grow" />
            <UiButton size="xs" variant="soft" icon="mdi-plus" :data-test="'ip-member-add-' + g.id" @click="addMember('ip', g.id)">Add member</UiButton>
            <UiButton size="xs" variant="text" icon="mdi-pencil-outline" @click="editGroup('ip', g)">Edit</UiButton>
            <UiButton size="xs" variant="text" color="error" icon="mdi-delete-outline" @click="removeGroup('ip', g)">Delete</UiButton>
          </UiToolbar>
          <UiDataTable :items="ipRows(g.id)" :columns="ipCols" :loading="!ipMembers[g.id]" caption="Members" empty-title="No members">
            <template #actions="{ row }">
              <UiButton size="xs" variant="text" icon="mdi-pencil-outline" icon-only label="Edit member" @click="editMember('ip', g.id, row)" />
              <UiButton size="xs" variant="text" color="error" icon="mdi-close" icon-only label="Remove member" @click="removeMember('ip', g.id, row)" />
            </template>
          </UiDataTable>
        </template>
      </UiAccordion>
    </template>
    <template v-else>
      <UiAlert v-if="hostGroups.error" kind="error" class="mb-3">{{ hostGroups.error }}</UiAlert>
      <UiEmptyState v-if="!hostGroups.items.length" title="No host groups" text="Group devices to target them together." />
      <UiAccordion v-else :items="hostItems" @toggle="toggleHost">
        <template v-for="g in hostGroups.items" :key="g.id" #[g.id]>
          <UiToolbar class="mb-2">
            <UiStatusChip :status="g.status" :colors="{ inactive: 'neutral' }" />
            <span class="grow" />
            <UiButton size="xs" variant="soft" icon="mdi-plus" :data-test="'host-member-add-' + g.id" @click="addMember('host', g.id)">Add device</UiButton>
            <UiButton size="xs" variant="text" icon="mdi-pencil-outline" @click="editGroup('host', g)">Edit</UiButton>
            <UiButton size="xs" variant="text" color="error" icon="mdi-delete-outline" @click="removeGroup('host', g)">Delete</UiButton>
          </UiToolbar>
          <UiDataTable :items="hostRows(g.id)" :columns="hostCols" :loading="!hostMembers[g.id]" caption="Members" empty-title="No members">
            <template #actions="{ row }">
              <UiButton size="xs" variant="text" icon="mdi-pencil-outline" icon-only label="Edit member" @click="editMember('host', g.id, row)" />
              <UiButton size="xs" variant="text" color="error" icon="mdi-close" icon-only label="Remove member" @click="removeMember('host', g.id, row)" />
            </template>
          </UiDataTable>
        </template>
      </UiAccordion>
    </template>

    <UiRecordDrawer v-model="groupDialog" close-on-save :title="(editingGroup ? 'Edit ' : 'New ') + (groupKind === 'ip' ? 'IP group' : 'host group')" :schema="groupSchema" :fields="groupFields" :initial="groupInitial" :submit="submitGroup" />
    <UiRecordDrawer v-model="memberDialog" close-on-save :title="editingMember ? 'Edit member' : 'Add member'" :schema="memberKind === 'ip' ? ipGroupMemberSchema : hostGroupMemberSchema" :fields="memberKind === 'ip' ? ipMemberFields : hostMemberFields" :initial="memberInitial" :submit="submitMember" />
  </UiPage>
</template>
