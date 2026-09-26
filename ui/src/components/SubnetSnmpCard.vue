<script setup lang="ts">
// SNMP credentials of one subnet (feature 021). Write-only: the card shows
// only whether credentials are configured, their version and where the
// effective ones come from; the form is never pre-filled and its values are
// dropped from component state after every submit.
import { computed, ref, watch } from 'vue'
import { useAbility } from '@casl/vue'
import { UiCard, UiButton, UiAlert, UiBadge, UiForm, UiInput, UiSelect, UiSecretField } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { useSubnets } from '@/stores/subnets'
import { snmpSchema, snmpTestSchema, protocolLabel, SNMP_AUTH_PROTOCOLS, SNMP_PRIV_PROTOCOLS, type SnmpFormInput } from '@/schemas'
import type { SNMPTestResult, Subnet, SubnetSNMPStatus } from '@/api/types'
import { describe } from '@/api/client'

const props = defineProps<{ subnet: Subnet }>()
const emit = defineEmits<{ (e: 'changed'): void }>()

const store = useSubnets()
const ability = useAbility()
const canManage = computed(() => ability.can('configure', 'SubnetSnmp'))
const canTest = computed(() => ability.can('test', 'SubnetSnmp'))

const status = ref<SubnetSNMPStatus | null>(null)
const testResult = ref<SNMPTestResult | null>(null)
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
watch(() => props.subnet.id, () => {
  testResult.value = null
  void load()
}, { immediate: true })

const blank = (): SnmpFormInput => ({ version: '2', community: '', user: '', security_level: 'authPriv', auth_protocol: 'SHA256', auth_password: '', priv_protocol: 'AES256', priv_password: '' })
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
// --- Test SNMP: one address of this subnet ---
const testForm = useZodForm(snmpTestSchema, {
  initial: { address: '' },
  onSubmit: (v) => store.testSnmp(props.subnet.id, v.address),
  onSuccess: (r: SNMPTestResult) => (testResult.value = r),
})
const OUTCOMES: Record<string, string> = {
  no_response: 'No response (timeout) — is SNMP enabled and reachable on UDP 161?',
  auth_failed: 'Credentials rejected (authentication failure)',
  unknown_user: 'Credentials rejected (unknown user)',
  privacy_failed: 'Credentials rejected (privacy settings / decryption failure)',
  no_credentials: 'No credentials configured for this subnet or its parents',
  credentials_unreadable: 'Credentials are configured but unreadable — re-enter them',
  error: 'SNMP error',
}
const testText = computed(() => {
  const r = testResult.value
  if (!r) return ''
  if (r.outcome === 'ok') return `Answered: ${r.sys_name || '(no sysName)'}${r.sys_descr ? ' — ' + r.sys_descr : ''} (${r.duration_ms} ms)`
  return OUTCOMES[r.outcome] ?? r.outcome
})

const versionOptions = [{ title: 'SNMP v2c (community)', value: '2' }, { title: 'SNMP v3 (user)', value: '3' }]
const levelOptions = [{ title: 'Authentication only (authNoPriv)', value: 'authNoPriv' }, { title: 'Authentication and privacy (authPriv)', value: 'authPriv' }]
const authOptions = SNMP_AUTH_PROTOCOLS.map((p) => ({ title: protocolLabel(p), value: p }))
const privOptions = SNMP_PRIV_PROTOCOLS.map((p) => ({ title: protocolLabel(p), value: p }))
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
            <template v-else>
              <UiSelect v-bind="form.field('security_level')" label="Security level" :options="levelOptions" required />
              <UiSecretField v-bind="form.field('user')" label="User" autocomplete="off" required />
              <UiSelect v-bind="form.field('auth_protocol')" label="Authentication protocol" :options="authOptions" required />
              <UiSecretField v-bind="form.field('auth_password')" label="Authentication password" hint="At least 8 characters" autocomplete="new-password" required />
              <template v-if="form.values.security_level === 'authPriv'">
                <UiSelect v-bind="form.field('priv_protocol')" label="Privacy protocol" :options="privOptions" required />
                <UiSecretField v-bind="form.field('priv_password')" label="Privacy password" hint="At least 8 characters" autocomplete="new-password" required />
              </template>
            </template>
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

    <div v-if="canTest" class="mt-4 border-t border-base-300 pt-3">
      <UiForm :form="testForm">
        <div class="flex flex-wrap items-end gap-2">
          <UiInput v-bind="testForm.field('address')" label="Test SNMP against" :placeholder="'an address in ' + subnet.cidr" size="sm" class="w-56" data-test="snmp-test-address" />
          <UiButton size="sm" variant="soft" icon="mdi-lan-check" :loading="testForm.submitting.value" data-test="snmp-test" @click="testForm.submit()">Test SNMP</UiButton>
        </div>
      </UiForm>
      <UiAlert v-if="testForm.serverError.value" kind="error" class="mt-2">{{ testForm.serverError.value }}</UiAlert>
      <UiAlert v-if="testResult" :kind="testResult.outcome === 'ok' ? 'success' : 'warning'" class="mt-2" data-test="snmp-test-result">{{ testText }}</UiAlert>
    </div>
  </UiCard>
</template>
