<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useSubnets } from '@/stores/subnets'
import type { SubnetFilter } from '@/stores/subnets'
import type { ScanResult, Subnet, SubnetTreeNode } from '@/api/types'
import { describe } from '@/api/client'

const router = useRouter()
const store = useSubnets()

const q = ref('')
const status = ref<string | null>(null)
const ipVersion = ref<string | null>(null)

const STATUSES = ['active', 'reserved', 'deprecated', 'deleted']
const VERSIONS = [
  { title: 'IPv4', value: '4' },
  { title: 'IPv6', value: '6' },
]

const statusColor: Record<string, string> = { active: 'success', reserved: 'info', deprecated: 'warning', deleted: 'grey' }

onMounted(() => {
  void store.list()
  void store.loadTree()
})

function reload(): void {
  const filter: SubnetFilter = {
    q: q.value.trim() || undefined,
    status: status.value ?? undefined,
    ip_version: ipVersion.value ? Number(ipVersion.value) : undefined,
  }
  void store.list(filter)
}

function utilPct(s: Subnet): number {
  if (typeof s.utilization === 'number') return Math.round(s.utilization * (s.utilization <= 1 ? 100 : 1))
  const total = s.total_addresses ?? 0
  return total > 0 ? Math.round(((s.used_addresses ?? 0) / total) * 100) : 0
}

function utilColor(pct: number): string {
  return pct >= 90 ? 'error' : pct >= 75 ? 'warning' : 'success'
}

function filterByTree(node: SubnetTreeNode): void {
  q.value = node.cidr
  reload()
}

// Flatten the tree for an indented navigator.
interface FlatNode { subnet: SubnetTreeNode; depth: number }
const flatTree = computed<FlatNode[]>(() => {
  const out: FlatNode[] = []
  const walk = (nodes: SubnetTreeNode[], depth: number): void => {
    for (const n of nodes) {
      out.push({ subnet: n, depth })
      if (n.children?.length) walk(n.children, depth + 1)
    }
  }
  walk(store.tree, 0)
  return out
})

const scanning = ref<string | null>(null)
const scanResult = ref<ScanResult | null>(null)
const actionError = ref('')

async function scan(s: Subnet): Promise<void> {
  scanning.value = s.id
  actionError.value = ''
  scanResult.value = null
  try {
    scanResult.value = await store.scan(s.id)
    await store.list()
  } catch (e) {
    actionError.value = describe(e)
  } finally {
    scanning.value = null
  }
}

async function removeSubnet(s: Subnet): Promise<void> {
  actionError.value = ''
  try {
    await store.remove(s.id)
    await store.loadTree()
  } catch (e) {
    actionError.value = describe(e)
  }
}
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <h1 class="text-h5">Subnets</h1>
      <v-spacer />
      <v-btn variant="text" icon="mdi-refresh" @click="reload" />
    </div>

    <v-row>
      <v-col cols="12" md="4">
        <v-card variant="tonal">
          <v-card-title class="text-subtitle-1">Tree</v-card-title>
          <v-card-text class="pt-0">
            <div v-for="n in flatTree" :key="n.subnet.id" class="d-flex align-center py-1 cursor-pointer" :style="{ paddingLeft: n.depth * 16 + 'px' }" @click="filterByTree(n.subnet)">
              <v-icon size="small" icon="mdi-ip-network-outline" class="me-2" />
              <span class="text-body-2">{{ n.subnet.cidr }}</span>
              <span class="text-caption text-medium-emphasis ms-2">{{ n.subnet.name }}</span>
            </div>
            <div v-if="!flatTree.length" class="text-medium-emphasis">No subnets yet.</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="8">
        <v-card variant="tonal" class="mb-4">
          <v-card-text>
            <v-row dense>
              <v-col cols="12" sm="6"><v-text-field v-model="q" label="Search (name / CIDR)" density="compact" clearable hide-details @keyup.enter="reload" @click:clear="reload" /></v-col>
              <v-col cols="6" sm="3"><v-select v-model="status" :items="STATUSES" label="Status" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
              <v-col cols="6" sm="3"><v-select v-model="ipVersion" :items="VERSIONS" label="Version" density="compact" clearable hide-details @update:model-value="reload" /></v-col>
            </v-row>
          </v-card-text>
        </v-card>

        <v-alert v-if="store.error" type="error" variant="tonal" density="compact" class="mb-3">{{ store.error }}</v-alert>
        <v-alert v-if="actionError" type="error" variant="tonal" density="compact" class="mb-3">{{ actionError }}</v-alert>
        <v-alert v-if="scanResult" type="success" variant="tonal" density="compact" class="mb-3">
          Scan complete: {{ scanResult.alive_count ?? 0 }} alive, {{ scanResult.new_count ?? 0 }} new.
        </v-alert>

        <v-table data-test="subnets-table">
          <thead>
            <tr><th>Name</th><th>CIDR</th><th>Ver</th><th>Status</th><th style="width: 220px">Utilization</th><th>Actions</th></tr>
          </thead>
          <tbody>
            <tr v-for="s in store.items" :key="s.id" :data-test="'subnet-row-' + s.id">
              <td>{{ s.name }}</td>
              <td class="text-medium-emphasis">{{ s.cidr }}</td>
              <td class="text-medium-emphasis">v{{ s.ip_version }}</td>
              <td><v-chip size="x-small" :color="statusColor[s.status]" variant="flat">{{ s.status }}</v-chip></td>
              <td>
                <div class="d-flex align-center">
                  <v-progress-linear :model-value="utilPct(s)" height="8" rounded :color="utilColor(utilPct(s))" />
                  <span class="ms-3 text-caption" style="min-width: 84px">{{ utilPct(s) }}% ({{ s.used_addresses ?? 0 }}/{{ s.total_addresses ?? 0 }})</span>
                </div>
              </td>
              <td>
                <v-btn size="x-small" variant="text" :loading="scanning === s.id" prepend-icon="mdi-radar" @click="scan(s)">Scan</v-btn>
                <v-btn size="x-small" variant="text" color="error" icon="mdi-delete-outline" @click="removeSubnet(s)" />
              </td>
            </tr>
            <tr v-if="!store.items.length && !store.loading"><td colspan="6" class="text-medium-emphasis">No subnets match.</td></tr>
          </tbody>
        </v-table>
      </v-col>
    </v-row>
  </div>
</template>

<style scoped>
.cursor-pointer { cursor: pointer; }
</style>
