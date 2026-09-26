<script setup lang="ts">
// SNMP credentials of one subnet (feature 021). Write-only: the card shows
// only whether credentials are configured, their version and where the
// effective ones come from; the form is never pre-filled and its values are
// dropped from component state after every submit.
import { computed, ref, watch } from 'vue'
import { useAbility } from '@casl/vue'
import { UiCard, UiButton, UiAlert, UiBadge, UiForm, UiSelect, UiSecretField } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { useSubnets } from '@/stores/subnets'
import { snmpSchema, type SnmpFormInput } from '@/schemas'
import type { Subnet, SubnetSNMPStatus } from '@/api/types'
import { describe } from '@/api/client'

const props = defineProps<{ subnet: Subnet }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const store = useSubnets()
const ability = useAbility()
const canManage = computed(() => ability.can('manage', 'SubnetSnmp'))

const status = ref<SubnetSNMPStatus | null>(null)
const error = ref('')
const editing = ref(false)

async function load(): Promise<void> {
  error.value = ''
  try {
    status.value = await store.snmpStatus(props.subnet.id)
  } catch (e) {
    error.value = describe(e)
  }
}
watch(() => props.subnet.id, load, { immediate: true })

const blank = (): SnmpFormInput => ({ version: '2', community: '' })
const form = useZodForm(snmpSchema, {
  initial: blank(),
  onSubmit: (body) => store.setSnmp(props.subnet.id, body),
  onSuccess: (s: SubnetSNMPStatus) => {
    status.value = s
    form.reset(blank())
    editing.value = false
    emit('changed')
  },
})
function startEdit(): void {
  form.reset(blank())
  editing.value = true
}
function cancel(): void {
  form.reset(blank())
  editing.value = false
}

const versionLabel = (v?: number) => (v === 3 ? 'v3' : v === 2 ? 'v2c' : '')
const summary = computed(() => {
  const e = status.value?.effective
  if (!e || e.state === 'none') return 'Not configured for this subnet or its parents'
  const kind = [`SNMP ${versionLabel(e.version)}`, e.security_level].filter(Boolean).join(' ')
  return e.state === 'own' ? `Own · ${kind}` : `Inherited from ${e.source_name} (${e.source_cidr}) · ${kind}`
})
const stateColor = computed(() => ({ own: 'success', inherited: 'info', none: 'neutral' } as const)[status.value?.effective.state ?? 'none'])
const versionOptions = [{ title: 'SNMP v2c (community)', value: '2' }]
</script>

<template>
  <UiCard title="SNMP credentials" data-test="snmp-card">
    <UiAlert v-if="error" kind="error" class="mb-3">{{ error }}</UiAlert>
    <div class="flex flex-wrap items-center gap-2 text-sm" data-test="snmp-status">
      <UiBadge :color="stateColor" soft size="sm">{{ status?.effective.state ?? 'none' }}</UiBadge>
      <span>{{ summary }}</span>
      <UiBadge v-if="status?.effective.weak" color="warning" size="xs" data-test="snmp-weak">weak protocol</UiBadge>
    </div>
    <p class="mt-2 text-xs text-base-content/70">Credentials are stored encrypted and are never shown again. Child subnets without their own inherit them.</p>

    <template v-if="canManage">
      <div v-if="!editing" class="mt-3 flex flex-wrap gap-2">
        <UiButton size="sm" variant="soft" icon="mdi-key-outline" data-test="snmp-set" @click="startEdit">{{ status?.own ? 'Replace' : 'Set' }}</UiButton>
      </div>
      <div v-else class="mt-3" data-test="snmp-form">
        <UiForm :form="form">
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <UiSelect v-bind="form.field('version')" label="Version" :options="versionOptions" required />
            <UiSecretField v-if="form.values.version === '2'" v-bind="form.field('community')" label="Community" autocomplete="new-password" required />
          </div>
        </UiForm>
        <UiAlert v-if="form.serverError.value" kind="error" class="mt-2">{{ form.serverError.value }}</UiAlert>
        <div class="mt-3 flex justify-end gap-2">
          <UiButton variant="text" @click="cancel">Cancel</UiButton>
          <UiButton :loading="form.submitting.value" data-test="snmp-save" @click="form.submit()">Save credentials</UiButton>
        </div>
      </div>
    </template>
    <p v-else class="mt-3 text-sm text-base-content/70" data-test="snmp-readonly">Only users who manage subnets can change SNMP credentials.</p>
  </UiCard>
</template>
