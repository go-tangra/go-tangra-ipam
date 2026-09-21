<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useGroups } from '@/stores/groups'
import { useHostGroups } from '@/stores/hostGroups'
import type { GroupMatch, HostGroupMember, IPGroupMember } from '@/api/types'
import { describe } from '@/api/client'

const groups = useGroups()
const hostGroups = useHostGroups()

const tab = ref('ip')
const statusColor: Record<string, string> = { active: 'success', inactive: 'grey' }

onMounted(() => {
  void groups.list()
  void hostGroups.list()
})

// --- check ip ---
const checkIp = ref('')
const matches = ref<GroupMatch[] | null>(null)
const checking = ref(false)
const checkError = ref('')

async function runCheck(): Promise<void> {
  if (!checkIp.value.trim()) return
  checking.value = true
  checkError.value = ''
  matches.value = null
  try {
    matches.value = await groups.checkIp(checkIp.value.trim())
  } catch (e) {
    checkError.value = describe(e)
  } finally {
    checking.value = false
  }
}

// --- member expansion ---
const ipMembers = ref<Record<string, IPGroupMember[]>>({})
const hostMembers = ref<Record<string, HostGroupMember[]>>({})

async function loadIpMembers(id: string): Promise<void> {
  ipMembers.value = { ...ipMembers.value, [id]: await groups.members(id) }
}
async function loadHostMembers(id: string): Promise<void> {
  hostMembers.value = { ...hostMembers.value, [id]: await hostGroups.members(id) }
}
</script>

<template>
  <div>
    <h1 class="text-h5 mb-4">Groups</h1>

    <v-card variant="tonal" class="mb-4">
      <v-card-text>
        <div class="d-flex align-center">
          <v-text-field
            v-model="checkIp" label="Check IP membership" placeholder="10.0.0.5"
            density="compact" hide-details style="max-width: 320px" @keyup.enter="runCheck"
          />
          <v-btn class="ms-3" color="primary" variant="tonal" :loading="checking" prepend-icon="mdi-magnify" @click="runCheck">Check</v-btn>
        </div>
        <v-alert v-if="checkError" type="error" variant="tonal" density="compact" class="mt-3">{{ checkError }}</v-alert>
        <div v-if="matches" class="mt-3">
          <template v-if="matches.length">
            <v-chip v-for="m in matches" :key="m.group_id" size="small" color="primary" variant="tonal" class="me-2 mb-2">
              {{ m.name ?? m.group_id }}<span v-if="m.value" class="ms-1 text-caption">({{ m.value }})</span>
            </v-chip>
          </template>
          <span v-else class="text-medium-emphasis">No groups contain that address.</span>
        </div>
      </v-card-text>
    </v-card>

    <v-tabs v-model="tab" class="mb-3">
      <v-tab value="ip">IP groups ({{ groups.items.length }})</v-tab>
      <v-tab value="host">Host groups ({{ hostGroups.items.length }})</v-tab>
    </v-tabs>

    <v-window v-model="tab">
      <v-window-item value="ip">
        <v-alert v-if="groups.error" type="error" variant="tonal" density="compact" class="mb-3">{{ groups.error }}</v-alert>
        <v-expansion-panels variant="accordion">
          <v-expansion-panel v-for="g in groups.items" :key="g.id" @group:selected="loadIpMembers(g.id)">
            <v-expansion-panel-title>
              <span>{{ g.name }}</span>
              <v-chip size="x-small" :color="statusColor[g.status]" variant="flat" class="ms-3">{{ g.status }}</v-chip>
              <v-spacer />
              <span class="text-caption text-medium-emphasis me-3">{{ g.member_count ?? 0 }} members</span>
            </v-expansion-panel-title>
            <v-expansion-panel-text>
              <v-table density="compact">
                <thead><tr><th>Type</th><th>Value</th><th>Description</th></tr></thead>
                <tbody>
                  <tr v-for="m in ipMembers[g.id] ?? []" :key="m.id">
                    <td>{{ m.member_type }}</td><td>{{ m.value }}</td><td class="text-medium-emphasis">{{ m.description || '—' }}</td>
                  </tr>
                  <tr v-if="!(ipMembers[g.id] ?? []).length"><td colspan="3" class="text-medium-emphasis">No members.</td></tr>
                </tbody>
              </v-table>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
        <div v-if="!groups.items.length" class="text-medium-emphasis">No IP groups.</div>
      </v-window-item>

      <v-window-item value="host">
        <v-alert v-if="hostGroups.error" type="error" variant="tonal" density="compact" class="mb-3">{{ hostGroups.error }}</v-alert>
        <v-expansion-panels variant="accordion">
          <v-expansion-panel v-for="g in hostGroups.items" :key="g.id" @group:selected="loadHostMembers(g.id)">
            <v-expansion-panel-title>
              <span>{{ g.name }}</span>
              <v-chip size="x-small" :color="statusColor[g.status]" variant="flat" class="ms-3">{{ g.status }}</v-chip>
              <v-spacer />
              <span class="text-caption text-medium-emphasis me-3">{{ g.member_count ?? 0 }} members</span>
            </v-expansion-panel-title>
            <v-expansion-panel-text>
              <v-table density="compact">
                <thead><tr><th>Device</th><th>Type</th><th>Status</th><th>Primary IP</th></tr></thead>
                <tbody>
                  <tr v-for="m in hostMembers[g.id] ?? []" :key="m.id">
                    <td>{{ m.device_name || m.device_id }}</td>
                    <td class="text-medium-emphasis">{{ m.device_type || '—' }}</td>
                    <td class="text-medium-emphasis">{{ m.device_status || '—' }}</td>
                    <td class="text-medium-emphasis">{{ m.device_primary_ip || '—' }}</td>
                  </tr>
                  <tr v-if="!(hostMembers[g.id] ?? []).length"><td colspan="4" class="text-medium-emphasis">No members.</td></tr>
                </tbody>
              </v-table>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
        <div v-if="!hostGroups.items.length" class="text-medium-emphasis">No host groups.</div>
      </v-window-item>
    </v-window>
  </div>
</template>
