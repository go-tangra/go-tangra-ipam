<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useAddresses } from '@/stores/addresses'
import { useSubnets } from '@/stores/subnets'
import { useLive } from '@/stores/live'
import type { AddressFilter } from '@/stores/addresses'
import type { IPAddress, PingResult } from '@/api/types'
import { describe } from '@/api/client'

const store = useAddresses()
const subnets = useSubnets()
const live = useLive()

const subnetId = ref<string | null>(null)
const status = ref<string | null>(null)
const addressType = ref<string | null>(null)
const hostname = ref('')

const STATUSES = ['active', 'reserved', 'dhcp', 'deprecated', 'offline']
const TYPES = ['host', 'gateway', 'broadcast', 'network', 'virtual', 'anycast']
const statusColor: Record<string, string> = { active: 'success', reserved: 'info', dhcp: 'teal', deprecated: 'warning', offline: 'grey' }

let release: (() => void) | null = null

onMounted(() => {
  void store.list()
  void subnets.list()
  release = live.connect()
})
onUnmounted(() => release?.())

const subnetItems = () => subnets.items.map((s) => ({ title: s.name + ' (' + s.cidr + ')', value: s.id }))

function reload(): void {
  const filter: AddressFilter = {
    subnet_id: subnetId.value ?? undefined,
    status: status.value ?? undefined,
    address_type: addressType.value ?? undefined,
    hostname: hostname.value.trim() || undefined,
  }
  void store.list(filter)
}

const actionError = ref('')

// --- allocate dialog ---
const allocOpen = ref(false)
const allocMode = ref<'single' | 'bulk' | 'suggest'>('single')
const allocSubnet = ref<string | null>(null)
const allocHostname = ref('')
const allocCount = ref(1)
const allocPrefix = ref('')
const suggested = ref<string[]>([])
const busy = ref(false)

function openAlloc(mode: 'single' | 'bulk' | 'suggest'): void {
  allocMode.value = mode
  allocSubnet.value = subnetId.value ?? (subnets.items[0]?.id ?? null)
  allocHostname.value = ''
  allocCount.value = 1
  allocPrefix.value = ''
  suggested.value = []
  actionError.value = ''
  allocOpen.value = true
}

async function runAlloc(): Promise<void> {
  if (!allocSubnet.value) return
  busy.value = true
  actionError.value = ''
  try {
    if (allocMode.value === 'single') {
      await store.allocate({ subnet_id: allocSubnet.value, hostname: allocHostname.value.trim() || undefined })
      allocOpen.value = false
    } else if (allocMode.value === 'bulk') {
      await store.bulkAllocate({ subnet_id: allocSubnet.value, count: allocCount.value, hostname_prefix: allocPrefix.value.trim() || undefined })
      allocOpen.value = false
    } else {
      suggested.value = await store.suggest(allocSubnet.value, allocCount.value)
    }
  } catch (e) {
    actionError.value = describe(e)
  } finally {
    busy.value = false
  }
}

// --- ping ---
const pingResult = ref<Record<string, PingResult>>({})
async function ping(a: IPAddress): Promise<void> {
  actionError.value = ''
  try {
    pingResult.value = { ...pingResult.value, [a.id]: await store.ping(a.id) }
  } catch (e) {
    actionError.value = describe(e)
  }
}

async function removeAddr(a: IPAddress): Promise<void> {
  actionError.value = ''
  try {
    await store.remove(a.id)
  } catch (e) {
    actionError.value = describe(e)
  }
}
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <h1 class="text-h5">IP Addresses</h1>
      <v-chip v-if="live.connected" size="x-small" color="success" variant="tonal" class="ms-3">live</v-chip>
      <v-spacer />
      <v-btn size="small" variant="tonal" prepend-icon="mdi-plus" class="me-2" @click="openAlloc('single')">Allocate</v-btn>
      <v-btn size="small" variant="tonal" prepend-icon="mdi-plus-box-multiple" class="me-2" @click="openAlloc('bulk')">Bulk</v-btn>
      <v-btn size="small" variant="tonal" prepend-icon="mdi-lightbulb-on-outline" @click="openAlloc('suggest')">Suggest</v-btn>
    </div>

    <v-card variant="tonal" class="mb-4">
      <v-card-text>
        <v-row dense>
          <v-col cols="12" sm="4"><v-select v-model="subnetId" :items="subnetItems()" label="Subnet" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
          <v-col cols="6" sm="3"><v-select v-model="status" :items="STATUSES" label="Status" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
          <v-col cols="6" sm="3"><v-select v-model="addressType" :items="TYPES" label="Type" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
          <v-col cols="12" sm="2"><v-text-field v-model="hostname" label="Hostname" density="compact" clearable hide-details @keyup.enter="reload" @click:clear="reload" /></v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-alert v-if="store.error" type="error" variant="tonal" density="compact" class="mb-3">{{ store.error }}</v-alert>
    <v-alert v-if="actionError" type="error" variant="tonal" density="compact" class="mb-3">{{ actionError }}</v-alert>

    <v-table data-test="addresses-table">
      <thead>
        <tr><th>Address</th><th>Hostname</th><th>MAC</th><th>Type</th><th>Status</th><th>Ping</th><th>Actions</th></tr>
      </thead>
      <tbody>
        <tr v-for="a in store.items" :key="a.id" :data-test="'address-row-' + a.id">
          <td>{{ a.address }}<v-chip v-if="a.is_primary" size="x-small" color="primary" variant="tonal" class="ms-2">primary</v-chip></td>
          <td class="text-medium-emphasis">{{ a.hostname || '—' }}</td>
          <td class="text-medium-emphasis">{{ a.mac_address || '—' }}</td>
          <td class="text-medium-emphasis">{{ a.address_type }}</td>
          <td><v-chip size="x-small" :color="statusColor[a.status]" variant="flat">{{ a.status }}</v-chip></td>
          <td>
            <v-chip v-if="pingResult[a.id]" size="x-small" :color="pingResult[a.id]!.alive ? 'success' : 'grey'" variant="tonal">
              {{ pingResult[a.id]!.alive ? (pingResult[a.id]!.rtt_ms ?? 0) + ' ms' : 'down' }}
            </v-chip>
            <span v-else class="text-medium-emphasis">—</span>
          </td>
          <td>
            <v-btn size="x-small" variant="text" prepend-icon="mdi-lan-pending" @click="ping(a)">Ping</v-btn>
            <v-btn size="x-small" variant="text" color="error" icon="mdi-delete-outline" @click="removeAddr(a)" />
          </td>
        </tr>
        <tr v-if="!store.items.length && !store.loading"><td colspan="7" class="text-medium-emphasis">No addresses match.</td></tr>
      </tbody>
    </v-table>

    <v-dialog v-model="allocOpen" max-width="480">
      <v-card>
        <v-card-title class="text-subtitle-1">
          {{ allocMode === 'single' ? 'Allocate next-free' : allocMode === 'bulk' ? 'Bulk allocate' : 'Suggest free addresses' }}
        </v-card-title>
        <v-card-text>
          <v-select v-model="allocSubnet" :items="subnetItems()" label="Subnet" density="compact" class="mb-3" hide-details />
          <v-text-field v-if="allocMode === 'single'" v-model="allocHostname" label="Hostname (optional)" density="compact" hide-details />
          <template v-if="allocMode === 'bulk'">
            <v-text-field v-model.number="allocCount" type="number" label="Count" density="compact" class="mb-3" hide-details />
            <v-text-field v-model="allocPrefix" label="Hostname prefix (optional)" density="compact" hide-details />
          </template>
          <template v-if="allocMode === 'suggest'">
            <v-text-field v-model.number="allocCount" type="number" label="Count" density="compact" hide-details />
            <div v-if="suggested.length" class="mt-3">
              <v-chip v-for="ip in suggested" :key="ip" size="small" variant="tonal" class="me-2 mb-2">{{ ip }}</v-chip>
            </div>
          </template>
          <v-alert v-if="actionError" type="error" variant="tonal" density="compact" class="mt-3">{{ actionError }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="allocOpen = false">Close</v-btn>
          <v-btn color="primary" variant="tonal" :loading="busy" @click="runAlloc">
            {{ allocMode === 'suggest' ? 'Suggest' : 'Allocate' }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>
