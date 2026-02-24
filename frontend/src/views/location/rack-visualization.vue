<script lang="ts" setup>
import { ref, computed, onMounted, watch } from 'vue';

import { Tooltip, Spin, Empty, Badge } from 'ant-design-vue';

import { type ipamservicev1_Device } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamDeviceStore } from '../../stores/ipam-device.state';

const props = defineProps<{
  locationId: string;
  rackSizeU: number;
}>();

const emit = defineEmits<{
  (e: 'slotClick', position: number): void;
  (e: 'deviceClick', device: ipamservicev1_Device): void;
}>();

const deviceStore = useIpamDeviceStore();
const loading = ref(false);
const devices = ref<ipamservicev1_Device[]>([]);
const unpositionedDevices = ref<ipamservicev1_Device[]>([]);

interface RackSlot {
  position: number;
  device?: ipamservicev1_Device;
  isOccupied: boolean;
  isPartOfDevice: boolean;
  isConflict: boolean;
  deviceStartPosition?: number;
}

const rackSlots = computed(() => {
  const slots: RackSlot[] = [];
  const occupiedMap = new Map<number, { device: ipamservicev1_Device; isStart: boolean }>();

  // Build occupancy map
  for (const device of devices.value) {
    if (device.rackPosition && device.deviceHeightU) {
      const startPos = device.rackPosition;
      const height = device.deviceHeightU;
      for (let i = 0; i < height; i++) {
        const pos = startPos + i;
        if (occupiedMap.has(pos)) {
          // Conflict detected
          occupiedMap.set(pos, { device, isStart: i === 0 });
        } else {
          occupiedMap.set(pos, { device, isStart: i === 0 });
        }
      }
    }
  }

  // Build slots array (from top to bottom, rack convention: top = highest U number)
  for (let i = props.rackSizeU; i >= 1; i--) {
    const occupied = occupiedMap.get(i);
    slots.push({
      position: i,
      device: occupied?.isStart ? occupied.device : undefined,
      isOccupied: !!occupied,
      isPartOfDevice: occupied && !occupied.isStart ? true : false,
      isConflict: false, // TODO: detect conflicts
      deviceStartPosition: occupied?.device?.rackPosition,
    });
  }

  return slots;
});

const usedU = computed(() => {
  let used = 0;
  for (const device of devices.value) {
    if (device.rackPosition && device.deviceHeightU) {
      used += device.deviceHeightU;
    }
  }
  return used;
});

const freeU = computed(() => props.rackSizeU - usedU.value);

async function loadDevices() {
  if (!props.locationId) return;

  loading.value = true;
  try {
    const resp = await deviceStore.listDevices(
      { page: 1, pageSize: 100 },
      { locationId: props.locationId },
    );
    const allDevices = resp.items ?? [];
    devices.value = allDevices.filter(
      (d) => d.rackPosition !== undefined && d.rackPosition !== null,
    );
    unpositionedDevices.value = allDevices.filter(
      (d) => d.rackPosition === undefined || d.rackPosition === null,
    );
  } catch (e) {
    console.error('Failed to load devices for rack:', e);
  } finally {
    loading.value = false;
  }
}

function handleSlotClick(slot: RackSlot) {
  if (slot.device) {
    emit('deviceClick', slot.device);
  } else if (!slot.isPartOfDevice) {
    emit('slotClick', slot.position);
  }
}

function getDeviceHeight(device: ipamservicev1_Device): number {
  return device.deviceHeightU ?? 1;
}

function getSlotClass(slot: RackSlot): string {
  if (slot.isConflict) return 'slot-conflict';
  if (slot.device) return 'slot-device cursor-pointer';
  if (slot.isPartOfDevice) return 'slot-device-part';
  return 'slot-empty cursor-pointer';
}

onMounted(() => {
  loadDevices();
});

watch(() => props.locationId, () => {
  loadDevices();
});

defineExpose({
  refresh: loadDevices,
});
</script>

<template>
  <div class="rack-visualization">
    <Spin :spinning="loading">
      <div v-if="devices.length === 0 && unpositionedDevices.length === 0 && !loading" class="mb-4">
        <Empty :description="$t('ui.table.noData')" />
      </div>

      <!-- Summary -->
      <div class="flex justify-between items-center mb-4 text-sm">
        <div class="flex gap-4">
          <span>
            <Badge status="processing" />
            {{ $t('ipam.page.rack.usedSlots', { used: usedU, total: rackSizeU }) }}
          </span>
          <span>
            <Badge status="success" />
            {{ $t('ipam.page.rack.freeSlots', { free: freeU }) }}
          </span>
        </div>
      </div>

      <!-- Legend -->
      <div class="flex gap-4 mb-4 text-xs">
        <div class="flex items-center gap-1">
          <div class="legend-available w-4 h-4 rounded"></div>
          <span>{{ $t('ipam.page.rack.available') }}</span>
        </div>
        <div class="flex items-center gap-1">
          <div class="legend-occupied w-4 h-4 rounded"></div>
          <span>{{ $t('ipam.page.rack.occupied') }}</span>
        </div>
      </div>

      <!-- Unpositioned Devices -->
      <div v-if="unpositionedDevices.length > 0" class="mb-4">
        <div class="text-sm font-medium mb-2">{{ $t('ipam.page.rack.unpositioned') }}</div>
        <div class="flex flex-col gap-1">
          <div
            v-for="device in unpositionedDevices"
            :key="device.id"
            class="unpositioned-device flex items-center gap-2 px-3 py-2 rounded cursor-pointer"
            @click="emit('deviceClick', device)"
          >
            <span class="font-medium truncate">{{ device.name }}</span>
            <span v-if="device.deviceHeightU" class="text-xs opacity-70">({{ device.deviceHeightU }}U)</span>
          </div>
        </div>
      </div>

      <!-- Rack Diagram -->
      <div class="rack-container rounded-lg overflow-hidden">
        <div
          v-for="slot in rackSlots"
          :key="slot.position"
          class="rack-slot flex items-center"
          :class="getSlotClass(slot)"
          @click="handleSlotClick(slot)"
        >
          <!-- U Number -->
          <div class="slot-number w-12 text-center text-xs font-mono py-2">
            {{ slot.position }}U
          </div>

          <!-- Slot Content -->
          <div class="flex-1 px-3 py-2 min-h-[32px] flex items-center">
            <template v-if="slot.device">
              <Tooltip :title="`${slot.device.name} (${getDeviceHeight(slot.device)}U)`">
                <div class="flex items-center gap-2 w-full">
                  <span class="font-medium truncate">{{ slot.device.name }}</span>
                  <span class="text-xs opacity-70">({{ getDeviceHeight(slot.device) }}U)</span>
                </div>
              </Tooltip>
            </template>
            <template v-else-if="slot.isPartOfDevice">
              <div class="h-full w-full flex items-center justify-center opacity-50">
                <!-- Continuation of device above -->
              </div>
            </template>
            <template v-else>
              <Tooltip :title="$t('ipam.page.rack.clickToAdd')">
                <div class="text-xs opacity-50 italic">
                  {{ $t('ipam.page.rack.available') }}
                </div>
              </Tooltip>
            </template>
          </div>
        </div>
      </div>
    </Spin>
  </div>
</template>

<style scoped>
.rack-container {
  max-height: 600px;
  overflow-y: auto;
  border: 1px solid var(--border-color, hsl(var(--border)));
  background-color: var(--card-bg, hsl(var(--card)));
}

.rack-slot {
  transition: background-color 0.2s ease;
  border-bottom: 1px solid var(--border-color, hsl(var(--border)));
}

.rack-slot:last-child {
  border-bottom: none;
}

.slot-number {
  border-right: 1px solid var(--border-color, hsl(var(--border)));
  background-color: hsl(var(--muted));
  color: hsl(var(--muted-foreground));
}

/* Legend boxes */
.legend-available {
  background-color: hsl(var(--muted));
  border: 1px solid hsl(var(--border));
}

.legend-occupied {
  background-color: hsl(var(--primary) / 0.2);
  border: 1px solid hsl(var(--primary));
}

/* Slot states */
.slot-empty {
  background-color: hsl(var(--muted) / 0.3);
}

.slot-empty:hover {
  background-color: hsl(var(--accent));
}

.slot-device {
  background-color: hsl(var(--primary) / 0.15);
  border-left: 3px solid hsl(var(--primary));
}

.slot-device:hover {
  background-color: hsl(var(--primary) / 0.25);
}

.slot-device-part {
  background-color: hsl(var(--primary) / 0.08);
  border-left: 3px solid hsl(var(--primary) / 0.5);
}

.slot-conflict {
  background-color: hsl(var(--destructive) / 0.15);
  border-left: 3px solid hsl(var(--destructive));
}

.unpositioned-device {
  background-color: hsl(var(--warning, 38 92% 50%) / 0.15);
  border-left: 3px solid hsl(var(--warning, 38 92% 50%));
}

.unpositioned-device:hover {
  background-color: hsl(var(--warning, 38 92% 50%) / 0.25);
}
</style>
