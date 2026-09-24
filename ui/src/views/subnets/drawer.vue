<script setup lang="ts">
// Right-hand subnet drawer, modelled on the go-tangra subnet drawer: one panel
// that shows a subnet and carries every action on it. The view mode lists the
// details, utilization, children and the latest scan; its toolbar switches the
// same drawer into edit, add-child or split mode, and back.
import { computed, onUnmounted, ref, shallowRef, watch } from 'vue'
import type { z } from 'zod'
import { UiDrawer, UiButton, UiAlert, UiKeyValueTable, UiStatusChip, UiRecordForm, UiNumberInput, UiForm, UiToolbar, useConfirm, type KeyValue } from '@go-tangra/ui'
import { useZodForm, zodToFields, type ZodForm } from '@go-tangra/ui/forms'
import { useSubnets } from '@/stores/subnets'
import { useVlans } from '@/stores/vlans'
import { useLocations } from '@/stores/locations'
import { useScans } from '@/stores/scans'
import { splitSchema, subnetSchema } from '@/schemas'
import type { IPScanJob, SplitResult, Subnet } from '@/api/types'
import { describe } from '@/api/client'
import { mergeEdit } from '@/api/merge'

export type SubnetDrawerMode = 'view' | 'create' | 'edit' | 'split'

const props = defineProps<{ subnetId: string | null; mode: SubnetDrawerMode; parentId?: string | undefined }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'changed'): void
  (e: 'navigate', id: string, mode: SubnetDrawerMode, parentId?: string): void
}>()

const store = useSubnets()
const vlans = useVlans()
const locations = useLocations()
const scans = useScans()
const confirm = useConfirm()
const open = computed(() => props.mode === 'create' || props.subnetId !== null)
const subnet = ref<Subnet | null>(null)
const error = ref('')

async function load(): Promise<void> {
  error.value = ''
  if (!props.subnetId) {
    subnet.value = null
    return
  }
  try {
    subnet.value = await store.get(props.subnetId)
  } catch (e) {
    error.value = describe(e)
  }
}

const byId = (id?: string) => (id ? store.items.find((s) => s.id === id) : undefined)
const parent = computed(() => byId(props.mode === 'create' ? props.parentId : subnet.value?.parent_id))
const children = computed(() => store.items.filter((s) => subnet.value && s.parent_id === subnet.value.id).sort((a, b) => a.cidr.localeCompare(b.cidr, undefined, { numeric: true })))
const vlanName = (id?: string) => { const v = vlans.items.find((x) => x.id === id); return v ? `${v.vlan_id} — ${v.name}` : '' }
const locationName = (id?: string) => locations.items.find((x) => x.id === id)?.name ?? ''

function utilPct(s: Subnet): number {
  if (typeof s.utilization === 'number') return Math.round(s.utilization * (s.utilization <= 1 ? 100 : 1))
  const total = s.total_addresses ?? 0
  return total > 0 ? Math.round(((s.used_addresses ?? 0) / total) * 100) : 0
}
const details = computed<KeyValue[]>(() => {
  const s = subnet.value
  if (!s) return []
  return [
    { label: 'CIDR', value: s.cidr, copyable: true }, { label: 'Version', value: 'IPv' + s.ip_version },
    { label: 'Network', value: s.network_address }, { label: 'Broadcast', value: s.broadcast_address },
    { label: 'Mask', value: s.mask }, { label: 'Gateway', value: s.gateway },
    { label: 'DNS servers', value: s.dns_servers }, { label: 'Parent', value: parent.value ? `${parent.value.cidr} — ${parent.value.name}` : '' },
    { label: 'VLAN', value: vlanName(s.vlan_id) }, { label: 'Location', value: locationName(s.location_id) },
  ]
})

// --- edit / create ---
const subnetKeys = Object.keys(subnetSchema.shape)
const fields = computed(() =>
  zodToFields(subnetSchema, {
    name: { cols: 6 },
    cidr: { label: 'CIDR', required: true, cols: 6, placeholder: parent.value ? 'inside ' + parent.value.cidr : '10.0.0.0/24' },
    parent_id: { label: 'Parent subnet', type: 'select', cols: 6, options: store.items.filter((s) => s.id !== subnet.value?.id).map((s) => ({ title: `${s.cidr} — ${s.name}`, value: s.id })), hint: 'Must contain this block.' },
    status: { cols: 6 },
    gateway: { cols: 6, placeholder: '10.0.0.1' },
    dns_servers: { label: 'DNS servers', cols: 6, placeholder: '10.0.0.53, 1.1.1.1' },
    vlan_id: { label: 'VLAN', type: 'select', cols: 6, options: vlans.items.map((v) => ({ title: `${v.vlan_id} — ${v.name}`, value: v.id })) },
    location_id: { label: 'Location', type: 'select', cols: 6, options: locations.items.map((l) => ({ title: `${l.name} (${l.location_type})`, value: l.id })) },
  }),
)
const initial = computed(() => {
  if (props.mode === 'edit' && subnet.value) return { ...subnet.value }
  const p = parent.value
  return p ? { parent_id: p.id, vlan_id: p.vlan_id, location_id: p.location_id, status: 'active' } : { status: 'active' }
})
const form = shallowRef<ZodForm<z.ZodType> | null>(null)
async function submit(v: Record<string, unknown>): Promise<Subnet> {
  return props.mode === 'edit' && subnet.value ? store.update(subnet.value.id, mergeEdit(subnet.value, v, subnetKeys)) : store.create(v)
}
function onSaved(s: unknown): void {
  emit('changed')
  emit('navigate', (s as Subnet).id, 'view')
}

// --- split ---
const preview = ref<SplitResult | null>(null)
const splitError = ref('')
const splitForm = useZodForm(splitSchema, {
  onSubmit: async (v) => {
    if (!subnet.value) return
    await store.split(subnet.value.id, v.prefix_length)
    emit('changed')
    emit('navigate', subnet.value.id, 'view')
  },
})
const maxBits = (s: Subnet) => (s.ip_version === 6 ? 128 : 32)
// Up to six halvings, stopping at /30 (v4) or /64 (v6).
const quickSplits = computed(() => {
  const s = subnet.value
  const p = s?.prefix_length
  if (!s || p === undefined) return []
  const limit = s.ip_version === 6 ? Math.max(64, p + 1) : 30
  const out: { prefix: number; count: number; hosts: string }[] = []
  for (let i = 1; i <= 6 && p + i <= Math.min(limit, maxBits(s)); i++) {
    const hostBits = maxBits(s) - (p + i)
    out.push({ prefix: p + i, count: 2 ** i, hosts: s.ip_version === 6 ? '2^' + hostBits : String(Math.max(0, 2 ** hostBits - 2)) })
  }
  return out
})
async function runPreview(): Promise<void> {
  const s = subnet.value
  const v = splitForm.validate()
  if (!s || !v) return
  splitError.value = ''
  try {
    preview.value = await store.split(s.id, v.prefix_length, true)
  } catch (e) {
    preview.value = null
    splitError.value = describe(e)
  }
}
function pickPrefix(prefix: number): void {
  splitForm.values.prefix_length = prefix
  void runPreview()
}
watch([() => props.mode, subnet], ([m, s]) => {
  if (m !== 'split' || !s) return
  preview.value = null
  splitError.value = ''
  splitForm.reset({ prefix_length: (s.prefix_length ?? 24) + 1 })
  void runPreview()
})

// --- scan: queue a job and follow it until it finishes ---
const scanJob = ref<IPScanJob | null>(null)
const TERMINAL = new Set(['completed', 'failed', 'cancelled'])
let poll: ReturnType<typeof setTimeout> | undefined
onUnmounted(() => clearTimeout(poll))
const scanning = computed(() => !!scanJob.value && !TERMINAL.has(scanJob.value.status))
async function scan(): Promise<void> {
  if (!subnet.value) return
  error.value = ''
  try {
    scanJob.value = await store.scan(subnet.value.id)
    follow()
  } catch (e) {
    error.value = describe(e)
  }
}
function follow(): void {
  poll = setTimeout(async () => {
    const job = scanJob.value
    if (!job) return
    try {
      scanJob.value = await scans.get(job.id)
    } catch (e) {
      error.value = describe(e)
      return
    }
    if (TERMINAL.has(scanJob.value.status)) {
      await load()
      emit('changed')
    } else follow()
  }, 1000)
}
const scanKind = computed(() => ({ completed: 'success', failed: 'error', cancelled: 'warning' } as const)[scanJob.value?.status as 'completed'] ?? 'info')
const scanText = computed(() => {
  const j = scanJob.value
  if (!j) return ''
  if (j.status === 'completed') return `Scan complete: ${j.alive_count ?? 0} alive, ${j.new_count ?? 0} new, ${j.updated_count ?? 0} updated.`
  if (j.status === 'failed') return `Scan failed: ${j.status_message || 'unknown error'}.`
  if (j.status === 'cancelled') return 'Scan cancelled.'
  return `Scanning… ${j.progress ?? 0}% (${j.scanned_count ?? 0}/${j.total_addresses ?? 0} probed, ${j.alive_count ?? 0} alive)`
})

// Declared after the scan state it resets (it runs immediately).
watch(() => props.subnetId, (id, prev) => {
  if (id !== prev) {
    clearTimeout(poll)
    scanJob.value = null
  }
  void load()
}, { immediate: true })

async function remove(): Promise<void> {
  const s = subnet.value
  if (!s) return
  if (!(await confirm.ask({ title: `Delete ${s.cidr}?`, text: 'Its addresses are released.', danger: true, confirmLabel: 'Delete' }))) return
  try {
    await store.remove(s.id)
    emit('changed')
    emit('close')
  } catch (e) {
    error.value = describe(e)
  }
}

const title = computed(() => {
  const s = subnet.value
  if (props.mode === 'create') return parent.value ? `New subnet in ${parent.value.cidr}` : 'New subnet'
  if (!s) return 'Subnet'
  return props.mode === 'edit' ? `Edit ${s.cidr}` : props.mode === 'split' ? `Split ${s.cidr}` : `${s.name} · ${s.cidr}`
})
const back = () => (subnet.value ? emit('navigate', subnet.value.id, 'view') : emit('close'))
</script>

<template>
  <UiDrawer :model-value="open" :title="title" size="lg" data-test="subnet-drawer" @update:model-value="emit('close')">
    <UiAlert v-if="error" kind="error" class="mb-4">{{ error }}</UiAlert>

    <!-- view -->
    <template v-if="mode === 'view' && subnet">
      <UiToolbar class="mb-4 flex-wrap">
        <UiButton size="sm" icon="mdi-radar" :loading="scanning" data-test="drawer-scan" @click="scan">Scan</UiButton>
        <UiButton size="sm" variant="soft" icon="mdi-call-split" data-test="drawer-split" @click="emit('navigate', subnet.id, 'split')">Split</UiButton>
        <UiButton size="sm" variant="soft" icon="mdi-plus-box-outline" data-test="drawer-add-child" @click="emit('navigate', '', 'create', subnet.id)">Add child</UiButton>
        <UiButton size="sm" variant="soft" icon="mdi-pencil-outline" data-test="drawer-edit" @click="emit('navigate', subnet.id, 'edit')">Edit</UiButton>
        <span class="grow" />
        <UiButton size="sm" variant="text" color="error" icon="mdi-delete-outline" data-test="drawer-delete" @click="remove">Delete</UiButton>
      </UiToolbar>
      <UiAlert v-if="scanJob" :kind="scanKind" class="mb-4" data-test="scan-status">
        <div class="flex w-full items-center gap-3">
          <span class="grow">{{ scanText }}</span>
          <progress v-if="scanning" class="progress progress-info h-2 w-28" :value="scanJob.progress ?? 0" max="100" aria-label="Scan progress" />
        </div>
      </UiAlert>

      <section class="mb-5">
        <div class="mb-1 flex items-center justify-between text-sm">
          <span class="font-medium">Utilization</span>
          <span class="flex items-center gap-2"><UiStatusChip :status="subnet.status" :colors="{ reserved: 'info', deprecated: 'warning', deleted: 'neutral' }" /> {{ utilPct(subnet) }}%</span>
        </div>
        <progress class="progress h-2 w-full" :class="utilPct(subnet) >= 90 ? 'progress-error' : utilPct(subnet) >= 75 ? 'progress-warning' : 'progress-success'" :value="utilPct(subnet)" max="100" aria-label="Utilization" />
        <p class="mt-1 text-xs text-base-content/70">{{ subnet.used_addresses ?? 0 }} used · {{ subnet.available_addresses ?? 0 }} available · {{ subnet.total_addresses ?? 0 }} total</p>
      </section>
      <p v-if="subnet.description" class="mb-4 text-sm">{{ subnet.description }}</p>
      <UiKeyValueTable :items="details" :columns="2" />

      <section class="mt-6">
        <h3 class="mb-2 text-sm font-medium">Child subnets ({{ children.length }})</h3>
        <p v-if="!children.length" class="text-sm text-base-content/70">None yet — use Split or Add child.</p>
        <ul v-else class="divide-y divide-base-300 rounded-box border border-base-300 text-sm">
          <li v-for="c in children" :key="c.id">
            <button type="button" class="flex w-full items-center gap-3 px-3 py-2 text-start hover:bg-base-200" @click="emit('navigate', c.id, 'view')">
              <span class="w-36 shrink-0 font-mono">{{ c.cidr }}</span>
              <span class="grow truncate">{{ c.name }}</span>
              <span class="text-xs text-base-content/70">{{ utilPct(c) }}%</span>
            </button>
          </li>
        </ul>
      </section>
    </template>

    <!-- create / edit -->
    <UiRecordForm v-else-if="mode === 'create' || (mode === 'edit' && subnet)" :key="mode + (subnet?.id ?? '')" :schema="subnetSchema" :fields="fields" :initial="initial" :submit="submit" @ready="form = $event" @saved="onSaved" />

    <!-- split -->
    <div v-else-if="mode === 'split' && subnet" class="flex flex-col gap-4" data-test="split-dialog">
      <p class="text-sm text-base-content/70">Carve <strong>{{ subnet.name }}</strong> into equal child subnets. Children inherit its VLAN and location; blocks that already exist are skipped.</p>
      <div v-if="quickSplits.length" class="flex flex-wrap gap-2" role="group" aria-label="Quick divisions">
        <UiButton v-for="q in quickSplits" :key="q.prefix" size="sm" :variant="splitForm.values.prefix_length === q.prefix ? 'solid' : 'soft'" @click="pickPrefix(q.prefix)">{{ q.count }} × /{{ q.prefix }} <span class="ms-1 text-xs opacity-70">({{ q.hosts }} hosts)</span></UiButton>
      </div>
      <UiForm :form="splitForm">
        <div class="flex items-end gap-2">
          <UiNumberInput v-bind="splitForm.field('prefix_length')" label="Child prefix length" :min="(subnet.prefix_length ?? 0) + 1" :max="maxBits(subnet)" class="w-48" />
          <UiButton variant="soft" icon="mdi-eye-outline" @click="runPreview">Preview</UiButton>
        </div>
      </UiForm>
      <UiAlert v-if="splitError" kind="error">{{ splitError }}</UiAlert>
      <div v-if="preview" class="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div>
          <h3 class="mb-1 text-sm font-medium">Will create ({{ preview.created.length }})</h3>
          <ul class="max-h-72 overflow-y-auto rounded-box border border-base-300 text-sm" data-test="split-created">
            <li v-for="c in preview.created" :key="c.cidr" class="flex justify-between border-b border-base-300 px-3 py-1 last:border-0"><span class="font-mono">{{ c.cidr }}</span><span class="text-xs text-base-content/70">{{ c.total_addresses }} addrs</span></li>
          </ul>
        </div>
        <div v-if="preview.skipped?.length">
          <h3 class="mb-1 text-sm font-medium">Skipped ({{ preview.skipped.length }})</h3>
          <ul class="max-h-72 overflow-y-auto rounded-box border border-base-300 text-sm">
            <li v-for="k in preview.skipped" :key="k.cidr" class="border-b border-base-300 px-3 py-1 last:border-0"><span class="font-mono">{{ k.cidr }}</span> <span class="text-xs text-base-content/70">{{ k.reason }}</span></li>
          </ul>
        </div>
      </div>
    </div>

    <template v-if="mode !== 'view'" #actions>
      <UiButton variant="text" color="neutral" icon="mdi-arrow-left" @click="back">{{ subnet ? 'Back' : 'Cancel' }}</UiButton>
      <UiButton v-if="mode === 'split'" icon="mdi-call-split" :disabled="!preview?.created.length" :loading="splitForm.submitting.value" data-test="split-confirm" @click="splitForm.submit()">Create {{ preview?.created.length ?? 0 }} subnets</UiButton>
      <UiButton v-else :loading="form?.submitting.value ?? false" data-test="drawer-save" @click="form?.submit()">Save</UiButton>
    </template>
  </UiDrawer>
</template>
