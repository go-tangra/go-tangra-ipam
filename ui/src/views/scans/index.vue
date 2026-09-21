<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useScans } from '@/stores/scans'
import { useSubnets } from '@/stores/subnets'
import { useLive } from '@/stores/live'
import type { IPScanJob } from '@/api/types'
import { describe } from '@/api/client'

const store = useScans()
const subnets = useSubnets()
const live = useLive()

const statusColor: Record<string, string> = { pending: 'grey', scanning: 'info', completed: 'success', failed: 'error', cancelled: 'grey' }

let release: (() => void) | null = null

onMounted(() => {
  void store.list()
  void subnets.list()
  release = live.connect()
})
onUnmounted(() => release?.())

const subnetItems = () => subnets.items.map((s) => ({ title: s.name + ' (' + s.cidr + ')', value: s.id }))

// --- start dialog ---
const startOpen = ref(false)
const startSubnet = ref<string | null>(null)
const enableSnmp = ref(false)
const enableDns = ref(false)
const busy = ref(false)
const actionError = ref('')

function openStart(): void {
  startSubnet.value = subnets.items[0]?.id ?? null
  enableSnmp.value = false
  enableDns.value = false
  actionError.value = ''
  startOpen.value = true
}

async function runStart(): Promise<void> {
  if (!startSubnet.value) return
  busy.value = true
  actionError.value = ''
  try {
    await store.start({ subnet_id: startSubnet.value, enable_snmp: enableSnmp.value, enable_dns_update: enableDns.value })
    startOpen.value = false
  } catch (e) {
    actionError.value = describe(e)
  } finally {
    busy.value = false
  }
}

async function cancel(job: IPScanJob): Promise<void> {
  actionError.value = ''
  try {
    await store.cancel(job.id)
  } catch (e) {
    actionError.value = describe(e)
  }
}

function subnetLabel(id: string): string {
  const s = subnets.items.find((x) => x.id === id)
  return s ? s.cidr : id
}
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <h1 class="text-h5">Discovery scans</h1>
      <v-chip v-if="live.connected" size="x-small" color="success" variant="tonal" class="ms-3">live</v-chip>
      <v-spacer />
      <v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-radar" @click="openStart">Start scan</v-btn>
    </div>

    <v-alert v-if="store.error" type="error" variant="tonal" density="compact" class="mb-3">{{ store.error }}</v-alert>
    <v-alert v-if="actionError" type="error" variant="tonal" density="compact" class="mb-3">{{ actionError }}</v-alert>

    <v-table data-test="scans-table">
      <thead>
        <tr><th>Subnet</th><th>Status</th><th style="width: 240px">Progress</th><th>Alive</th><th>New</th><th>Actions</th></tr>
      </thead>
      <tbody>
        <tr v-for="j in store.items" :key="j.id" :data-test="'scan-row-' + j.id">
          <td class="text-medium-emphasis">{{ subnetLabel(j.subnet_id) }}</td>
          <td><v-chip size="x-small" :color="statusColor[j.status]" variant="flat">{{ j.status }}</v-chip></td>
          <td>
            <div class="d-flex align-center">
              <v-progress-linear
                :model-value="j.progress"
                :indeterminate="j.status === 'scanning' && !j.progress"
                height="8" rounded :color="statusColor[j.status]"
              />
              <span class="ms-3 text-caption" style="min-width: 40px">{{ j.progress }}%</span>
            </div>
          </td>
          <td class="text-medium-emphasis">{{ j.alive_count ?? 0 }}</td>
          <td class="text-medium-emphasis">{{ j.new_count ?? 0 }}</td>
          <td>
            <v-btn
              v-if="j.status === 'pending' || j.status === 'scanning'"
              size="x-small" variant="text" color="error" prepend-icon="mdi-cancel"
              @click="cancel(j)"
            >Cancel</v-btn>
          </td>
        </tr>
        <tr v-if="!store.items.length && !store.loading"><td colspan="6" class="text-medium-emphasis">No scans yet.</td></tr>
      </tbody>
    </v-table>

    <v-dialog v-model="startOpen" max-width="420">
      <v-card>
        <v-card-title class="text-subtitle-1">Start discovery scan</v-card-title>
        <v-card-text>
          <v-select v-model="startSubnet" :items="subnetItems()" label="Subnet" density="compact" hide-details class="mb-3" />
          <v-switch v-model="enableSnmp" label="SNMP discovery" density="compact" hide-details />
          <v-switch v-model="enableDns" label="Update DNS" density="compact" hide-details />
          <v-alert v-if="actionError" type="error" variant="tonal" density="compact" class="mt-3">{{ actionError }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="startOpen = false">Cancel</v-btn>
          <v-btn color="primary" variant="tonal" :loading="busy" @click="runStart">Start</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>
