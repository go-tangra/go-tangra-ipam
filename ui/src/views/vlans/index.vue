<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useVlans } from '@/stores/vlans'
import type { VlanFilter } from '@/stores/vlans'

const store = useVlans()

const domain = ref('')
const status = ref<string | null>(null)
const STATUSES = ['active', 'reserved', 'deprecated']
const statusColor: Record<string, string> = { active: 'success', reserved: 'info', deprecated: 'warning' }

onMounted(() => void store.list())

function reload(): void {
  const filter: VlanFilter = {
    domain: domain.value.trim() || undefined,
    status: status.value ?? undefined,
  }
  void store.list(filter)
}
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <h1 class="text-h5">VLANs</h1>
      <v-spacer />
      <v-btn variant="text" icon="mdi-refresh" @click="reload" />
    </div>

    <v-card variant="tonal" class="mb-4">
      <v-card-text>
        <v-row dense>
          <v-col cols="12" sm="6"><v-text-field v-model="domain" label="Domain" density="compact" clearable hide-details @keyup.enter="reload" @click:clear="reload" /></v-col>
          <v-col cols="12" sm="6"><v-select v-model="status" :items="STATUSES" label="Status" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-alert v-if="store.error" type="error" variant="tonal" density="compact" class="mb-3">{{ store.error }}</v-alert>

    <v-table data-test="vlans-table">
      <thead>
        <tr><th>VLAN ID</th><th>Name</th><th>Domain</th><th>Status</th><th>Subnets</th></tr>
      </thead>
      <tbody>
        <tr v-for="v in store.items" :key="v.id" :data-test="'vlan-row-' + v.id">
          <td>{{ v.vlan_id }}</td>
          <td>{{ v.name }}</td>
          <td class="text-medium-emphasis">{{ v.domain || '—' }}</td>
          <td><v-chip size="x-small" :color="statusColor[v.status]" variant="flat">{{ v.status }}</v-chip></td>
          <td class="text-medium-emphasis">{{ v.subnet_count ?? 0 }}</td>
        </tr>
        <tr v-if="!store.items.length && !store.loading"><td colspan="5" class="text-medium-emphasis">No VLANs match.</td></tr>
      </tbody>
    </v-table>
  </div>
</template>
