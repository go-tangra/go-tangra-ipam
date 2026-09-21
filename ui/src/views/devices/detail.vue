<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useDevices } from '@/stores/devices'
import type { Device, DeviceInterface, DevicePackage, IPAddress } from '@/api/types'
import { describe } from '@/api/client'
import IpmiKvm from './ipmi-kvm.vue'

const route = useRoute()
const router = useRouter()
const store = useDevices()

const id = String(route.params.id)
const device = ref<Device | null>(null)
const error = ref('')
const tab = ref('interfaces')

const interfaces = ref<DeviceInterface[]>([])
const packages = ref<DevicePackage[]>([])
const addresses = ref<IPAddress[]>([])
const syncing = ref(false)

const statusColor: Record<string, string> = { active: 'success', planned: 'info', staged: 'teal', decommissioned: 'grey', offline: 'grey', failed: 'error', available: 'blue-grey' }

async function loadAll(): Promise<void> {
  error.value = ''
  try {
    device.value = await store.get(id)
    ;[interfaces.value, packages.value, addresses.value] = await Promise.all([
      store.interfaces(id),
      store.packages(id),
      store.addresses(id),
    ])
  } catch (e) {
    error.value = describe(e)
  }
}

async function syncPackages(): Promise<void> {
  syncing.value = true
  error.value = ''
  try {
    packages.value = await store.syncPackages(id)
  } catch (e) {
    error.value = describe(e)
  } finally {
    syncing.value = false
  }
}

onMounted(loadAll)
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <v-btn variant="text" icon="mdi-arrow-left" @click="router.push({ name: 'ipam-devices' })" />
      <h1 class="text-h5 ms-2">{{ device?.name ?? 'Device' }}</h1>
      <v-chip v-if="device" size="small" :color="statusColor[device.status]" variant="flat" class="ms-3">{{ device.status }}</v-chip>
      <v-spacer />
      <v-btn variant="text" icon="mdi-refresh" @click="loadAll" />
    </div>

    <v-alert v-if="error" type="error" variant="tonal" density="compact" class="mb-3">{{ error }}</v-alert>

    <v-card v-if="device" variant="tonal" class="mb-4">
      <v-card-text>
        <v-row dense>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">Type</div>{{ device.device_type }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">Manufacturer</div>{{ device.manufacturer || '—' }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">Model</div>{{ device.model || '—' }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">Management IP</div>{{ device.management_ip || '—' }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">OS</div>{{ device.os_type }} {{ device.os_version }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">Firmware</div>{{ device.firmware_version || '—' }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">Rack position</div>{{ device.rack_position ?? '—' }}</v-col>
          <v-col cols="6" sm="3"><div class="text-caption text-medium-emphasis">BMC</div>{{ device.ipmi_secret_ref ? 'configured' : 'none' }}</v-col>
        </v-row>
      </v-card-text>
    </v-card>

    <v-tabs v-model="tab" class="mb-3">
      <v-tab value="interfaces">Interfaces ({{ interfaces.length }})</v-tab>
      <v-tab value="packages">Packages ({{ packages.length }})</v-tab>
      <v-tab value="addresses">Addresses ({{ addresses.length }})</v-tab>
      <v-tab value="oob">Power / KVM</v-tab>
    </v-tabs>

    <v-window v-model="tab">
      <v-window-item value="interfaces">
        <v-table>
          <thead><tr><th>Name</th><th>MAC</th><th>Type</th><th>Speed</th><th>Neighbor</th></tr></thead>
          <tbody>
            <tr v-for="i in interfaces" :key="i.id">
              <td>{{ i.name }}</td>
              <td class="text-medium-emphasis">{{ i.mac_address || '—' }}</td>
              <td class="text-medium-emphasis">{{ i.interface_type || '—' }}</td>
              <td class="text-medium-emphasis">{{ i.speed_mbps ? i.speed_mbps + ' Mbps' : '—' }}</td>
              <td class="text-medium-emphasis">{{ i.remote_port_name || '—' }}</td>
            </tr>
            <tr v-if="!interfaces.length"><td colspan="5" class="text-medium-emphasis">No interfaces.</td></tr>
          </tbody>
        </v-table>
      </v-window-item>

      <v-window-item value="packages">
        <div class="d-flex mb-2">
          <v-spacer />
          <v-btn size="small" variant="tonal" prepend-icon="mdi-sync" :loading="syncing" @click="syncPackages">Sync</v-btn>
        </div>
        <v-table>
          <thead><tr><th>Package</th><th>Current</th><th>Available</th><th>Status</th></tr></thead>
          <tbody>
            <tr v-for="p in packages" :key="p.name">
              <td>{{ p.name }}</td>
              <td class="text-medium-emphasis">{{ p.current_version || '—' }}</td>
              <td class="text-medium-emphasis">{{ p.available_version || '—' }}</td>
              <td>
                <v-chip v-if="p.is_security_update" size="x-small" color="error" variant="tonal">security</v-chip>
                <v-chip v-else-if="p.needs_update" size="x-small" color="warning" variant="tonal">update</v-chip>
                <v-chip v-else size="x-small" color="success" variant="tonal">current</v-chip>
              </td>
            </tr>
            <tr v-if="!packages.length"><td colspan="4" class="text-medium-emphasis">No packages.</td></tr>
          </tbody>
        </v-table>
      </v-window-item>

      <v-window-item value="addresses">
        <v-table>
          <thead><tr><th>Address</th><th>Hostname</th><th>Type</th><th>Status</th></tr></thead>
          <tbody>
            <tr v-for="a in addresses" :key="a.id">
              <td>{{ a.address }}<v-chip v-if="a.is_primary" size="x-small" color="primary" variant="tonal" class="ms-2">primary</v-chip></td>
              <td class="text-medium-emphasis">{{ a.hostname || '—' }}</td>
              <td class="text-medium-emphasis">{{ a.address_type }}</td>
              <td class="text-medium-emphasis">{{ a.status }}</td>
            </tr>
            <tr v-if="!addresses.length"><td colspan="4" class="text-medium-emphasis">No addresses.</td></tr>
          </tbody>
        </v-table>
      </v-window-item>

      <v-window-item value="oob">
        <IpmiKvm :device-id="id" />
      </v-window-item>
    </v-window>
  </div>
</template>
