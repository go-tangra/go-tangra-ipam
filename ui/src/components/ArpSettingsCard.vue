<script setup lang="ts">
// Per-tenant ARP collection (feature 022, US3): scans with SNMP discovery read
// the ARP tables of the network devices that answer; administrators turn it
// off, exclude devices as sources and tune the proxy-ARP threshold.
import { computed, onMounted, ref } from 'vue'
import { useAbility } from '@casl/vue'
import { UiCard, UiAlert, UiButton, UiForm, UiSwitch, UiNumberInput, UiCheckbox } from '@go-tangra/ui'
import { useZodForm } from '@go-tangra/ui/forms'
import { useArp } from '@/stores/arp'
import { useDevices } from '@/stores/devices'
import { arpSettingsSchema } from '@/schemas'
import type { Device } from '@/api/types'
import { describe } from '@/api/client'

const store = useArp()
const devices = useDevices()
const ability = useAbility()
const canManage = computed(() => ability.can('configure', 'ArpSettings'))
const message = ref('')
const error = ref('')
const network = ref<Device[]>([])
const NETWORK_TYPES = new Set(['router', 'switch', 'firewall', 'load_balancer'])

const form = useZodForm(arpSettingsSchema, {
  onSubmit: (v) => store.save(v),
  onSuccess: () => (message.value = 'ARP settings saved'),
})
const excluded = computed<string[]>(() => (form.values.excluded_devices as string[] | undefined) ?? [])
function toggle(id: string, on: boolean): void {
  const cur = excluded.value.filter((x) => x !== id)
  form.values.excluded_devices = on ? [...cur, id] : cur
}
function reset(): void {
  const s = store.settings
  form.reset({ enabled: s?.enabled ?? true, proxy_threshold: s?.proxy_threshold ?? 8, excluded_devices: [...(s?.excluded_devices ?? [])] })
}
onMounted(async () => {
  try {
    await store.load()
    const all = await devices.lookup()
    const ids = new Set(store.settings?.excluded_devices ?? [])
    network.value = all.filter((d) => NETWORK_TYPES.has(d.device_type) || ids.has(d.id))
  } catch (e) {
    error.value = describe(e)
  }
  reset()
})
</script>

<template>
  <UiCard data-test="arp-settings">
    <h2 class="mb-1 text-base font-semibold">ARP-based MAC linking</h2>
    <p class="mb-3 text-sm text-base-content/70">
      Scans with SNMP discovery read the ARP and neighbour tables of the routers, firewalls and layer-3 switches that answer,
      and record each active address's MAC so agentless hosts can be linked to their switch port. MACs reported by the agent
      or entered manually are never overwritten.
    </p>
    <UiAlert v-if="store.error || error" kind="error" class="mb-3">{{ store.error || error }}</UiAlert>
    <UiAlert v-if="message" kind="success" class="mb-3" data-test="arp-message">{{ message }}</UiAlert>
    <UiForm :form="form" data-test="arp-form">
      <div class="flex flex-col gap-3">
        <UiSwitch v-bind="form.field('enabled')" label="Collect ARP tables during scans" :disabled="!canManage" />
        <UiNumberInput v-bind="form.field('proxy_threshold')" label="Ignore a MAC answering for more than (IPs per scan)" :min="2" :max="256" :disabled="!canManage" required />
        <fieldset>
          <legend class="mb-1 text-sm">Never use as ARP source</legend>
          <p v-if="!network.length" class="text-sm text-base-content/70">No network devices discovered yet.</p>
          <div class="grid grid-cols-1 gap-1 md:grid-cols-2">
            <div v-for="d in network" :key="d.id" :data-test="'arp-exclude-' + d.id">
              <UiCheckbox :id="'arp-exclude-' + d.id" :model-value="excluded.includes(d.id)" :label="d.name + ' (' + d.device_type + ')'" :disabled="!canManage" @update:model-value="(v: boolean) => toggle(d.id, v)" />
            </div>
          </div>
        </fieldset>
      </div>
    </UiForm>
    <div v-if="canManage" class="mt-3 flex justify-end gap-2">
      <UiButton variant="text" @click="reset">Reset</UiButton>
      <UiButton :loading="form.submitting.value" data-test="arp-save" @click="form.submit()">Save</UiButton>
    </div>
    <p v-else class="mt-3 text-sm text-base-content/70" data-test="arp-readonly">Only administrators who manage subnets can change these settings.</p>
  </UiCard>
</template>
