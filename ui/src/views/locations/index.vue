<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useLocations } from '@/stores/locations'
import type { Location, LocationTreeNode } from '@/api/types'

const store = useLocations()
const selected = ref<Location | null>(null)

const typeIcon: Record<string, string> = {
  region: 'mdi-earth', country: 'mdi-flag', city: 'mdi-city', datacenter: 'mdi-server',
  building: 'mdi-office-building', floor: 'mdi-layers', room: 'mdi-door', rack: 'mdi-rack',
  site: 'mdi-map-marker', branch: 'mdi-source-branch',
}
const statusColor: Record<string, string> = { active: 'success', planned: 'info', decommissioned: 'grey' }

onMounted(() => {
  void store.loadTree()
  void store.list()
})

interface FlatNode { loc: LocationTreeNode; depth: number }
const flatTree = computed<FlatNode[]>(() => {
  const out: FlatNode[] = []
  const walk = (nodes: LocationTreeNode[], depth: number): void => {
    for (const n of nodes) {
      out.push({ loc: n, depth })
      if (n.children?.length) walk(n.children, depth + 1)
    }
  }
  walk(store.tree, 0)
  return out
})

// Simple rack visualization: RACK-type locations expose rack_size_u; render an
// ordered stack of U slots (top = highest U). Device placement would come from
// the devices in the rack; here we render the empty rack frame with its size.
const rackUnits = computed<number[]>(() => {
  const size = selected.value?.rack_size_u ?? 0
  return Array.from({ length: size }, (_, i) => size - i)
})
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <h1 class="text-h5">Locations</h1>
      <v-spacer />
      <v-btn variant="text" icon="mdi-refresh" @click="store.loadTree()" />
    </div>

    <v-alert v-if="store.error" type="error" variant="tonal" density="compact" class="mb-3">{{ store.error }}</v-alert>

    <v-row>
      <v-col cols="12" md="5">
        <v-card variant="tonal">
          <v-card-title class="text-subtitle-1">Tree</v-card-title>
          <v-card-text class="pt-0">
            <div
              v-for="n in flatTree" :key="n.loc.id"
              class="d-flex align-center py-1 cursor-pointer"
              :style="{ paddingLeft: n.depth * 16 + 'px' }"
              @click="selected = n.loc"
            >
              <v-icon size="small" :icon="typeIcon[n.loc.location_type] ?? 'mdi-map-marker'" class="me-2" />
              <span class="text-body-2">{{ n.loc.name }}</span>
              <v-chip size="x-small" variant="tonal" class="ms-2">{{ n.loc.location_type }}</v-chip>
              <span v-if="n.loc.device_count" class="text-caption text-medium-emphasis ms-2">{{ n.loc.device_count }} dev</span>
            </div>
            <div v-if="!flatTree.length" class="text-medium-emphasis">No locations yet.</div>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="7">
        <v-card v-if="selected" variant="tonal">
          <v-card-title class="text-subtitle-1 d-flex align-center">
            {{ selected.name }}
            <v-chip size="x-small" :color="statusColor[selected.status]" variant="flat" class="ms-3">{{ selected.status }}</v-chip>
          </v-card-title>
          <v-card-text>
            <v-row dense class="mb-2">
              <v-col cols="6" sm="4"><div class="text-caption text-medium-emphasis">Type</div>{{ selected.location_type }}</v-col>
              <v-col cols="6" sm="4"><div class="text-caption text-medium-emphasis">Code</div>{{ selected.code || '—' }}</v-col>
              <v-col cols="6" sm="4"><div class="text-caption text-medium-emphasis">Path</div>{{ selected.path || '—' }}</v-col>
              <v-col cols="6" sm="4"><div class="text-caption text-medium-emphasis">Devices</div>{{ selected.device_count ?? 0 }}</v-col>
              <v-col cols="6" sm="4"><div class="text-caption text-medium-emphasis">Subnets</div>{{ selected.subnet_count ?? 0 }}</v-col>
              <v-col cols="6" sm="4"><div class="text-caption text-medium-emphasis">Rack size</div>{{ selected.rack_size_u ? selected.rack_size_u + 'U' : '—' }}</v-col>
            </v-row>

            <div v-if="selected.location_type === 'rack' && rackUnits.length">
              <div class="text-subtitle-2 mb-2">Rack elevation</div>
              <div class="rack">
                <div v-for="u in rackUnits" :key="u" class="rack-u">
                  <span class="rack-u-label">{{ u }}</span>
                  <span class="rack-u-slot" />
                </div>
              </div>
            </div>
            <div v-else-if="selected.location_type === 'rack'" class="text-medium-emphasis">No rack size set.</div>
          </v-card-text>
        </v-card>
        <v-card v-else variant="tonal">
          <v-card-text class="text-medium-emphasis">Select a location to view details.</v-card-text>
        </v-card>
      </v-col>
    </v-row>
  </div>
</template>

<style scoped>
.cursor-pointer { cursor: pointer; }
.rack { border: 2px solid rgba(var(--v-border-color), 0.4); border-radius: 4px; max-width: 320px; }
.rack-u { display: flex; align-items: center; height: 20px; border-bottom: 1px solid rgba(var(--v-border-color), 0.2); }
.rack-u:last-child { border-bottom: 0; }
.rack-u-label { width: 32px; text-align: right; padding-right: 8px; font-size: 10px; color: rgba(var(--v-theme-on-surface), 0.5); }
.rack-u-slot { flex: 1; height: 14px; margin: 0 4px; background: rgba(var(--v-theme-on-surface), 0.04); border-radius: 2px; }
</style>
