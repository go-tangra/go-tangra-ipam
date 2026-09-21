<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useDevices } from '@/stores/devices'
import type { DeviceFilter } from '@/stores/devices'

const router = useRouter()
const store = useDevices()

const q = ref('')
const deviceType = ref<string | null>(null)
const status = ref<string | null>(null)

const TYPES = ['server', 'vm', 'router', 'switch', 'firewall', 'load_balancer', 'access_point', 'storage', 'printer', 'phone', 'workstation', 'container', 'other']
const STATUSES = ['active', 'planned', 'staged', 'decommissioned', 'offline', 'failed', 'available']
const statusColor: Record<string, string> = { active: 'success', planned: 'info', staged: 'teal', decommissioned: 'grey', offline: 'grey', failed: 'error', available: 'blue-grey' }

onMounted(() => void store.list())

function reload(): void {
  const filter: DeviceFilter = {
    q: q.value.trim() || undefined,
    device_type: deviceType.value ?? undefined,
    status: status.value ?? undefined,
  }
  void store.list(filter)
}

function open(id: string): void {
  void router.push({ name: 'ipam-device', params: { id } })
}
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <h1 class="text-h5">Devices</h1>
      <v-spacer />
      <v-btn variant="text" icon="mdi-refresh" @click="reload" />
    </div>

    <v-card variant="tonal" class="mb-4">
      <v-card-text>
        <v-row dense>
          <v-col cols="12" sm="6"><v-text-field v-model="q" label="Search (name)" density="compact" clearable hide-details @keyup.enter="reload" @click:clear="reload" /></v-col>
          <v-col cols="6" sm="3"><v-select v-model="deviceType" :items="TYPES" label="Type" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
          <v-col cols="6" sm="3"><v-select v-model="status" :items="STATUSES" label="Status" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-alert v-if="store.error" type="error" variant="tonal" density="compact" class="mb-3">{{ store.error }}</v-alert>

    <v-table data-test="devices-table">
      <thead>
        <tr><th>Name</th><th>Type</th><th>Model</th><th>Management IP</th><th>Status</th><th>Updates</th><th>NICs</th></tr>
      </thead>
      <tbody>
        <tr v-for="d in store.items" :key="d.id" class="cursor-pointer" :data-test="'device-row-' + d.id" @click="open(d.id)">
          <td>{{ d.name }}</td>
          <td class="text-medium-emphasis">{{ d.device_type }}</td>
          <td class="text-medium-emphasis">{{ d.manufacturer }} {{ d.model }}</td>
          <td class="text-medium-emphasis">{{ d.management_ip || d.primary_ip || '—' }}</td>
          <td><v-chip size="x-small" :color="statusColor[d.status]" variant="flat">{{ d.status }}</v-chip></td>
          <td>
            <v-chip v-if="(d.security_update_count ?? 0) > 0" size="x-small" color="error" variant="tonal" class="me-1">{{ d.security_update_count }} sec</v-chip>
            <v-chip v-if="(d.package_update_count ?? 0) > 0" size="x-small" color="warning" variant="tonal">{{ d.package_update_count }} pkg</v-chip>
            <span v-if="!(d.package_update_count ?? 0) && !(d.security_update_count ?? 0)" class="text-medium-emphasis">—</span>
          </td>
          <td class="text-medium-emphasis">{{ d.interface_count ?? 0 }}</td>
        </tr>
        <tr v-if="!store.items.length && !store.loading"><td colspan="7" class="text-medium-emphasis">No devices match.</td></tr>
      </tbody>
    </v-table>
  </div>
</template>

<style scoped>
.cursor-pointer { cursor: pointer; }
</style>
