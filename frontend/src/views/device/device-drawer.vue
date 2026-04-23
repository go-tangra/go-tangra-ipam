<script lang="ts" setup>
import { ref, computed, watch } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { useUserStore } from 'shell/vben/stores';

import {
  Form,
  FormItem,
  Input,
  InputNumber,
  Button,
  notification,
  Textarea,
  Select,
  Descriptions,
  DescriptionsItem,
  Tag,
  Alert,
  Divider,
  Table,
} from 'ant-design-vue';

import { type ipamservicev1_Device } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamDeviceStore } from '../../stores/ipam-device.state';
import { useIpamLocationStore } from '../../stores/ipam-location.state';

const deviceStore = useIpamDeviceStore();
const locationStore = useIpamLocationStore();
const userStore = useUserStore();

// Rack position validation
const rackDevices = ref<ipamservicev1_Device[]>([]);
const loadingRackDevices = ref(false);

const data = ref<{
  mode: 'create' | 'edit' | 'view';
  row?: ipamservicev1_Device;
}>();
const loading = ref(false);

interface LocationOption {
  value: string;
  label: string;
  locationType?: string;
  rackSizeU?: number;
}

const locations = ref<LocationOption[]>([]);

const deviceTypeOptions = computed(() => [
  { value: 'DEVICE_TYPE_SERVER', label: $t('ipam.enum.deviceType.server') },
  { value: 'DEVICE_TYPE_ROUTER', label: $t('ipam.enum.deviceType.router') },
  { value: 'DEVICE_TYPE_SWITCH', label: $t('ipam.enum.deviceType.switch') },
  { value: 'DEVICE_TYPE_FIREWALL', label: $t('ipam.enum.deviceType.firewall') },
  { value: 'DEVICE_TYPE_LOAD_BALANCER', label: $t('ipam.enum.deviceType.loadBalancer') },
  { value: 'DEVICE_TYPE_VM', label: $t('ipam.enum.deviceType.virtualMachine') },
  { value: 'DEVICE_TYPE_CONTAINER', label: $t('ipam.enum.deviceType.container') },
  { value: 'DEVICE_TYPE_OTHER', label: $t('ipam.enum.deviceType.other') },
]);

const statusOptions = computed(() => [
  { value: 'DEVICE_STATUS_ACTIVE', label: $t('ipam.enum.deviceStatus.active') },
  { value: 'DEVICE_STATUS_INACTIVE', label: $t('ipam.enum.deviceStatus.inactive') },
  { value: 'DEVICE_STATUS_MAINTENANCE', label: $t('ipam.enum.deviceStatus.maintenance') },
  { value: 'DEVICE_STATUS_DECOMMISSIONED', label: $t('ipam.enum.deviceStatus.decommissioned') },
]);

const formState = ref<{
  name: string;
  description: string;
  deviceType: string;
  status: string;
  locationId?: string;
  serialNumber: string;
  manufacturer: string;
  model: string;
  rackPosition?: number;
  deviceHeightU?: number;
  managementIp: string;
}>({
  name: '',
  description: '',
  deviceType: 'DEVICE_TYPE_SERVER',
  status: 'DEVICE_STATUS_ACTIVE',
  locationId: undefined,
  serialNumber: '',
  manufacturer: '',
  model: '',
  rackPosition: undefined,
  deviceHeightU: 1,
  managementIp: '',
});

// Physical device types that can only be placed in rack locations
const physicalDeviceTypes = new Set([
  'DEVICE_TYPE_SERVER',
  'DEVICE_TYPE_ROUTER',
  'DEVICE_TYPE_SWITCH',
  'DEVICE_TYPE_FIREWALL',
  'DEVICE_TYPE_LOAD_BALANCER',
]);

const isPhysicalDevice = computed(() => physicalDeviceTypes.has(formState.value.deviceType));

const filteredLocations = computed(() => {
  if (isPhysicalDevice.value) {
    return locations.value.filter((l) => l.locationType === 'LOCATION_TYPE_RACK');
  }
  return locations.value;
});

const selectedLocation = computed(() => {
  if (!formState.value.locationId) return null;
  return locations.value.find((l) => l.value === formState.value.locationId);
});

const isRackLocation = computed(() => {
  return selectedLocation.value?.locationType === 'LOCATION_TYPE_RACK';
});

const rackSizeU = computed(() => {
  return selectedLocation.value?.rackSizeU ?? 42;
});

const maxRackPosition = computed(() => {
  const height = formState.value.deviceHeightU ?? 1;
  return rackSizeU.value - height + 1;
});

// Build a set of occupied U positions (excluding current device when editing)
const occupiedPositions = computed(() => {
  const occupied = new Set<number>();
  const currentDeviceId = data.value?.row?.id;

  for (const device of rackDevices.value) {
    // Skip current device when editing
    if (currentDeviceId && device.id === currentDeviceId) continue;

    if (device.rackPosition && device.deviceHeightU) {
      for (let i = 0; i < device.deviceHeightU; i++) {
        occupied.add(device.rackPosition + i);
      }
    }
  }
  return occupied;
});

// Check if the selected position would conflict with existing devices
const positionConflict = computed(() => {
  if (!formState.value.rackPosition || !isRackLocation.value) return null;

  const startPos = formState.value.rackPosition;
  const height = formState.value.deviceHeightU ?? 1;
  const conflicts: number[] = [];

  for (let i = 0; i < height; i++) {
    const pos = startPos + i;
    if (occupiedPositions.value.has(pos)) {
      conflicts.push(pos);
    }
  }

  return conflicts.length > 0 ? conflicts : null;
});

const positionConflictMessage = computed(() => {
  if (!positionConflict.value) return null;
  const positions = positionConflict.value.map((p) => `U${p}`).join(', ');
  return $t('ipam.page.rack.positionConflict', { positions });
});

async function loadRackDevices(locationId: string) {
  loadingRackDevices.value = true;
  try {
    const resp = await deviceStore.listDevices(
      { page: 1, pageSize: 100 },
      { locationId },
    );
    rackDevices.value = (resp.items ?? []).filter(
      (d) => d.rackPosition !== undefined && d.rackPosition !== null,
    );
  } catch (e) {
    console.error('Failed to load rack devices:', e);
    rackDevices.value = [];
  } finally {
    loadingRackDevices.value = false;
  }
}

// Watch for location changes and load rack devices
watch(
  () => formState.value.locationId,
  async (newLocationId) => {
    if (newLocationId && isRackLocation.value) {
      await loadRackDevices(newLocationId);
    } else {
      rackDevices.value = [];
    }
  },
);

// Clear location when device type changes and current selection is no longer valid
watch(
  () => formState.value.deviceType,
  () => {
    if (formState.value.locationId) {
      const stillValid = filteredLocations.value.some((l) => l.value === formState.value.locationId);
      if (!stillValid) {
        formState.value.locationId = undefined;
      }
    }
  },
);

const title = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.device.create');
    case 'edit':
      return $t('ipam.page.device.edit');
    default:
      return $t('ipam.page.device.view');
  }
});

const isCreateMode = computed(() => data.value?.mode === 'create');
const isEditMode = computed(() => data.value?.mode === 'edit');
const isViewMode = computed(() => data.value?.mode === 'view');

function deviceTypeToName(deviceType: string | undefined) {
  const option = deviceTypeOptions.value.find((o) => o.value === deviceType);
  return option?.label ?? deviceType ?? '';
}

function statusToColor(status: string | undefined) {
  switch (status) {
    case 'DEVICE_STATUS_ACTIVE':
      return '#52C41A';
    case 'DEVICE_STATUS_INACTIVE':
      return '#8C8C8C';
    case 'DEVICE_STATUS_MAINTENANCE':
      return '#FAAD14';
    case 'DEVICE_STATUS_DECOMMISSIONED':
      return '#FF4D4F';
    default:
      return '#C9CDD4';
  }
}

function statusToName(status: string | undefined) {
  const option = statusOptions.value.find((o) => o.value === status);
  return option?.label ?? status ?? '';
}

async function loadLocations() {
  try {
    const resp = await locationStore.listLocations({ page: 1, pageSize: 100 });
    locations.value = (resp.items ?? []).map((l) => ({
      value: l.id ?? '',
      label: l.name ?? '',
      locationType: l.locationType,
      rackSizeU: l.rackSizeU,
    }));
  } catch (e) {
    console.error('Failed to load locations:', e);
  }
}

async function handleSubmit() {
  // Check for position conflicts before submitting
  if (positionConflict.value) {
    notification.error({
      message: positionConflictMessage.value ?? $t('ipam.page.rack.positionConflict', { positions: '' }),
    });
    return;
  }

  loading.value = true;
  try {
    const rackPosition = isRackLocation.value ? formState.value.rackPosition : undefined;
    const deviceHeightU = isRackLocation.value ? (formState.value.deviceHeightU ?? 1) : undefined;
    // When location is a rack, use the locationId as rackId
    const rackId = isRackLocation.value ? formState.value.locationId : undefined;
    const managementIp = formState.value.managementIp.trim() || undefined;

    if (isCreateMode.value) {
      await deviceStore.createDevice(
        userStore.tenantId as number,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          deviceType: formState.value.deviceType as any,
          status: formState.value.status as any,
          locationId: formState.value.locationId,
          rackId,
          rackPosition,
          deviceHeightU,
          serialNumber: formState.value.serialNumber || undefined,
          manufacturer: formState.value.manufacturer || undefined,
          model: formState.value.model || undefined,
          managementIp,
        },
      );
      notification.success({
        message: $t('ipam.page.device.createSuccess'),
      });
    } else if (isEditMode.value && data.value?.row?.id) {
      const updateMask = ['name', 'description', 'deviceType', 'status', 'locationId', 'serialNumber', 'manufacturer', 'model', 'managementIp'];
      if (isRackLocation.value) {
        updateMask.push('rackId', 'rackPosition', 'deviceHeightU');
      }
      await deviceStore.updateDevice(
        data.value.row.id,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          deviceType: formState.value.deviceType as any,
          status: formState.value.status as any,
          locationId: formState.value.locationId,
          rackId,
          rackPosition,
          deviceHeightU,
          serialNumber: formState.value.serialNumber || undefined,
          manufacturer: formState.value.manufacturer || undefined,
          model: formState.value.model || undefined,
          managementIp,
        },
        updateMask,
      );
      notification.success({
        message: $t('ipam.page.device.updateSuccess'),
      });
    }
    drawerApi.close();
  } catch (e) {
    console.error('Failed to save device:', e);
    notification.error({
      message: isCreateMode.value
        ? $t('ui.notification.create_failed')
        : $t('ui.notification.update_failed'),
    });
  } finally {
    loading.value = false;
  }
}

function resetForm() {
  formState.value = {
    name: '',
    description: '',
    deviceType: 'DEVICE_TYPE_SERVER',
    status: 'DEVICE_STATUS_ACTIVE',
    locationId: undefined,
    serialNumber: '',
    manufacturer: '',
    model: '',
    rackPosition: undefined,
    deviceHeightU: 1,
    managementIp: '',
  };
}

const [Drawer, drawerApi] = useVbenDrawer({
  onCancel() {
    drawerApi.close();
  },

  async onOpenChange(isOpen) {
    if (isOpen) {
      data.value = drawerApi.getData() as {
        mode: 'create' | 'edit' | 'view';
        row?: ipamservicev1_Device;
      };

      await loadLocations();

      if (data.value?.mode === 'create') {
        resetForm();
        rackDevices.value = [];
      } else if (data.value?.row) {
        formState.value = {
          name: data.value.row.name ?? '',
          description: data.value.row.description ?? '',
          deviceType: data.value.row.deviceType ?? 'DEVICE_TYPE_SERVER',
          status: data.value.row.status ?? 'DEVICE_STATUS_ACTIVE',
          locationId: data.value.row.locationId,
          serialNumber: data.value.row.serialNumber ?? '',
          manufacturer: data.value.row.manufacturer ?? '',
          model: data.value.row.model ?? '',
          rackPosition: data.value.row.rackPosition ?? undefined,
          deviceHeightU: data.value.row.deviceHeightU ?? 1,
          managementIp: data.value.row.managementIp ?? '',
        };

        // Load rack devices for validation if editing a device in a rack
        if (data.value.row.locationId) {
          const location = locations.value.find((l) => l.value === data.value?.row?.locationId);
          if (location?.locationType === 'LOCATION_TYPE_RACK') {
            await loadRackDevices(data.value.row.locationId);
          }
        }
      }
    } else {
      rackDevices.value = [];
    }
  },
});

const device = computed(() => data.value?.row);

// Parse metadata JSON from the device
interface BoardMetadata {
  name?: string;
  vendor?: string;
  version?: string;
  serial?: string;
  bios_vendor?: string;
  bios_version?: string;
  bios_date?: string;
  sys_vendor?: string;
  product_name?: string;
  chassis_type?: string;
}

interface MemoryMetadata {
  type?: string;
  speed?: number;
  size?: number;
}

interface DeviceMetadata {
  machine_id?: string;
  arch?: string;
  kernel?: string;
  cpu_model?: string;
  cpu_count?: number;
  memory_total?: number;
  vm_type?: string;
  board?: BoardMetadata;
  memory?: MemoryMetadata;
  disks?: { name: string; type: string; model: string; size: number }[];
  interfaces?: { name: string; mac_address: string; ips: string[]; cidrs?: string[] }[];
}

function isVirtualDevice(deviceType: string | undefined) {
  return deviceType === 'DEVICE_TYPE_VM' || deviceType === 'DEVICE_TYPE_VIRTUAL_MACHINE';
}

function isContainerDevice(deviceType: string | undefined) {
  return deviceType === 'DEVICE_TYPE_CONTAINER';
}

function deviceTypeToColor(deviceType: string | undefined) {
  if (isVirtualDevice(deviceType)) return 'purple';
  if (isContainerDevice(deviceType)) return 'cyan';
  switch (deviceType) {
    case 'DEVICE_TYPE_SERVER':
      return 'blue';
    case 'DEVICE_TYPE_ROUTER':
    case 'DEVICE_TYPE_SWITCH':
      return 'green';
    case 'DEVICE_TYPE_FIREWALL':
      return 'orange';
    default:
      return 'default';
  }
}

const parsedMetadata = computed<DeviceMetadata | null>(() => {
  if (!device.value?.metadata) return null;
  try {
    return JSON.parse(device.value.metadata) as DeviceMetadata;
  } catch {
    return null;
  }
});

function formatBytes(bytes: number | undefined): string {
  if (!bytes) return '-';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let val = bytes;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
}

const diskColumns = [
  { title: 'Name', dataIndex: 'name', key: 'name' },
  { title: 'Type', dataIndex: 'type', key: 'type', width: 80 },
  { title: 'Size', dataIndex: 'size', key: 'size', width: 100, customRender: ({ text }: { text: number }) => formatBytes(text) },
  { title: 'Model', dataIndex: 'model', key: 'model' },
];

const interfaceColumns = [
  { title: 'Name', dataIndex: 'name', key: 'name', width: 100 },
  { title: 'MAC Address', dataIndex: 'mac_address', key: 'mac_address', width: 160 },
  { title: 'IPs', dataIndex: 'ips', key: 'ips', customRender: ({ text }: { text: string[] }) => (text ?? []).join(', ') || '-' },
  { title: 'CIDRs', dataIndex: 'cidrs', key: 'cidrs', customRender: ({ text }: { text: string[] }) => (text ?? []).join(', ') || '-' },
];
</script>

<template>
  <Drawer :title="title" :footer="false">
    <!-- View Mode -->
    <template v-if="device && isViewMode">
      <!-- Basic Info -->
      <Descriptions :column="1" bordered size="small">
        <DescriptionsItem :label="$t('ipam.page.device.name')">
          {{ device.name }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.device.deviceType')">
          <Tag :color="deviceTypeToColor(device.deviceType)">
            {{ deviceTypeToName(device.deviceType) }}
          </Tag>
          <span v-if="parsedMetadata?.vm_type" class="ml-2" style="color: #8c8c8c;">
            ({{ parsedMetadata.vm_type }})
          </span>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.device.status')">
          <Tag :color="statusToColor(device.status)">
            {{ statusToName(device.status) }}
          </Tag>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.device.description')">
          {{ device.description || '-' }}
        </DescriptionsItem>
      </Descriptions>

      <!-- System Information -->
      <template v-if="device.osType || device.osVersion || device.firmwareVersion">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionSystem') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="device.osType" :label="$t('ipam.page.device.osType')">
            {{ device.osType }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.osVersion" :label="$t('ipam.page.device.osVersion')">
            {{ device.osVersion }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.firmwareVersion" :label="$t('ipam.page.device.firmwareVersion')">
            {{ device.firmwareVersion }}
          </DescriptionsItem>
        </Descriptions>
      </template>

      <!-- Network -->
      <template v-if="device.primaryIp || device.primaryIpv6 || device.managementIp">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionNetwork') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="device.primaryIp" :label="$t('ipam.page.device.primaryIp')">
            <Tag color="blue">{{ device.primaryIp }}</Tag>
          </DescriptionsItem>
          <DescriptionsItem v-if="device.primaryIpv6" :label="$t('ipam.page.device.primaryIpv6')">
            <Tag color="blue">{{ device.primaryIpv6 }}</Tag>
          </DescriptionsItem>
          <DescriptionsItem v-if="device.managementIp" :label="$t('ipam.page.device.managementIp')">
            <Tag color="purple">{{ device.managementIp }}</Tag>
          </DescriptionsItem>
          <DescriptionsItem v-if="device.interfaceCount" :label="$t('ipam.page.device.interfaceCount')">
            {{ device.interfaceCount }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.addressCount" :label="$t('ipam.page.device.addressCount')">
            {{ device.addressCount }}
          </DescriptionsItem>
        </Descriptions>
      </template>

      <!-- Hardware -->
      <template v-if="device.manufacturer || device.model || device.serialNumber">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionHardware') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="device.manufacturer" :label="$t('ipam.page.device.manufacturer')">
            {{ device.manufacturer }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.model" :label="$t('ipam.page.device.model')">
            {{ device.model }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.serialNumber" :label="$t('ipam.page.device.serialNumber')">
            {{ device.serialNumber }}
          </DescriptionsItem>
        </Descriptions>
      </template>

      <!-- Board / BIOS (from metadata) -->
      <template v-if="parsedMetadata?.board && (parsedMetadata.board.vendor || parsedMetadata.board.sys_vendor || parsedMetadata.board.bios_vendor)">
        <Divider orientation="left">Board / BIOS</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="parsedMetadata.board.sys_vendor" label="System Vendor">
            {{ parsedMetadata.board.sys_vendor }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.product_name" label="Product">
            {{ parsedMetadata.board.product_name }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.vendor" label="Board Vendor">
            {{ parsedMetadata.board.vendor }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.name" label="Board Name">
            {{ parsedMetadata.board.name }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.serial" label="Board Serial">
            <code>{{ parsedMetadata.board.serial }}</code>
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.bios_vendor" label="BIOS Vendor">
            {{ parsedMetadata.board.bios_vendor }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.bios_version" label="BIOS Version">
            {{ parsedMetadata.board.bios_version }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.bios_date" label="BIOS Date">
            {{ parsedMetadata.board.bios_date }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.board.chassis_type" label="Chassis Type">
            {{ parsedMetadata.board.chassis_type }}
          </DescriptionsItem>
        </Descriptions>
      </template>

      <!-- Location -->
      <template v-if="device.rackPosition || device.deviceHeightU">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionLocation') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem
            v-if="device.rackPosition"
            :label="$t('ipam.page.device.rackPosition')"
          >
            {{ device.rackPosition }}U
          </DescriptionsItem>
          <DescriptionsItem
            v-if="device.deviceHeightU"
            :label="$t('ipam.page.device.deviceHeightU')"
          >
            {{ device.deviceHeightU }}U
          </DescriptionsItem>
        </Descriptions>
      </template>

      <!-- Host Details (parsed metadata) -->
      <template v-if="parsedMetadata">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionMetadata') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="parsedMetadata.machine_id" :label="$t('ipam.page.device.machineId')">
            <code>{{ parsedMetadata.machine_id }}</code>
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.arch" :label="$t('ipam.page.device.arch')">
            {{ parsedMetadata.arch }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.kernel" :label="$t('ipam.page.device.kernel')">
            {{ parsedMetadata.kernel }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.cpu_model" :label="$t('ipam.page.device.cpuModel')">
            {{ parsedMetadata.cpu_model }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.cpu_count" :label="$t('ipam.page.device.cpuCount')">
            {{ parsedMetadata.cpu_count }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.memory_total" :label="$t('ipam.page.device.memoryTotal')">
            {{ formatBytes(parsedMetadata.memory_total) }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata.memory?.type" label="Memory Type">
            {{ parsedMetadata.memory.type }}
            <span v-if="parsedMetadata.memory.speed" style="color: #8c8c8c;">
              @ {{ parsedMetadata.memory.speed }} MT/s
            </span>
          </DescriptionsItem>
        </Descriptions>

        <!-- Disks table -->
        <template v-if="parsedMetadata.disks && parsedMetadata.disks.length > 0">
          <Divider orientation="left" plain>{{ $t('ipam.page.device.disks') }}</Divider>
          <Table
            :columns="diskColumns"
            :data-source="parsedMetadata.disks"
            :pagination="false"
            size="small"
            bordered
            :row-key="(r: any) => r.name"
          />
        </template>

        <!-- Interfaces table -->
        <template v-if="parsedMetadata.interfaces && parsedMetadata.interfaces.length > 0">
          <Divider orientation="left" plain>{{ $t('ipam.page.device.interfaces') }}</Divider>
          <Table
            :columns="interfaceColumns"
            :data-source="parsedMetadata.interfaces"
            :pagination="false"
            size="small"
            bordered
            :row-key="(r: any) => r.name"
          />
        </template>
      </template>

      <!-- Notes / Contact / Tags -->
      <template v-if="device.contact || device.notes || device.tags">
        <Divider />
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="device.contact" :label="$t('ipam.page.device.contact')">
            {{ device.contact }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.notes" :label="$t('ipam.page.device.notes')">
            {{ device.notes }}
          </DescriptionsItem>
          <DescriptionsItem v-if="device.tags" :label="$t('ipam.page.device.tags')">
            {{ device.tags }}
          </DescriptionsItem>
        </Descriptions>
      </template>
    </template>

    <!-- Create/Edit Mode -->
    <template v-else-if="isCreateMode || isEditMode">
      <Form layout="vertical" :model="formState" @finish="handleSubmit">
        <FormItem
          :label="$t('ipam.page.device.name')"
          name="name"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Input
            v-model:value="formState.name"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.deviceType')" name="deviceType">
          <Select
            v-model:value="formState.deviceType"
            :options="deviceTypeOptions"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.status')" name="status">
          <Select
            v-model:value="formState.status"
            :options="statusOptions"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.location.title')" name="locationId">
          <Select
            v-model:value="formState.locationId"
            :options="filteredLocations"
            :placeholder="$t('ui.placeholder.select')"
            allow-clear
            show-search
            :filter-option="(input: string, option: any) =>
              option.label.toLowerCase().includes(input.toLowerCase())"
          />
        </FormItem>

        <template v-if="isRackLocation">
          <Alert
            type="info"
            :message="$t('ipam.page.rack.rackInfo', { size: rackSizeU })"
            show-icon
            class="mb-4"
          />

          <Alert
            v-if="positionConflict"
            type="error"
            :message="positionConflictMessage"
            show-icon
            class="mb-4"
          />

          <FormItem
            :label="$t('ipam.page.device.rackPosition')"
            name="rackPosition"
            :validate-status="positionConflict ? 'error' : undefined"
          >
            <InputNumber
              v-model:value="formState.rackPosition"
              :min="1"
              :max="maxRackPosition"
              :placeholder="'1'"
              style="width: 100%"
              :status="positionConflict ? 'error' : undefined"
            />
            <div class="text-gray-500 text-xs mt-1">
              {{ $t('ipam.page.device.rackPositionHelp') }}
            </div>
          </FormItem>

          <FormItem :label="$t('ipam.page.device.deviceHeightU')" name="deviceHeightU">
            <InputNumber
              v-model:value="formState.deviceHeightU"
              :min="1"
              :max="10"
              :placeholder="'1'"
              style="width: 100%"
            />
            <div class="text-gray-500 text-xs mt-1">
              {{ $t('ipam.page.device.deviceHeightUHelp') }}
            </div>
          </FormItem>
        </template>

        <FormItem
          :label="$t('ipam.page.device.managementIp')"
          name="managementIp"
          :rules="[
            {
              pattern: /^(?:(?:\d{1,3}\.){3}\d{1,3}|[0-9a-fA-F:]+)?$/,
              message: $t('ipam.page.device.managementIpInvalid'),
              trigger: 'blur',
            },
          ]"
        >
          <Input
            v-model:value="formState.managementIp"
            placeholder="192.0.2.10"
            :maxlength="45"
            allow-clear
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.manufacturer')" name="manufacturer">
          <Input
            v-model:value="formState.manufacturer"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.model')" name="model">
          <Input
            v-model:value="formState.model"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.serialNumber')" name="serialNumber">
          <Input
            v-model:value="formState.serialNumber"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.description')" name="description">
          <Textarea
            v-model:value="formState.description"
            :rows="3"
            :maxlength="1024"
            :placeholder="$t('ui.placeholder.input')"
          />
        </FormItem>

        <FormItem>
          <Button type="primary" html-type="submit" :loading="loading" block>
            {{ isCreateMode ? $t('ui.button.create', { moduleName: '' }) : $t('ui.button.save') }}
          </Button>
        </FormItem>
      </Form>
    </template>
  </Drawer>
</template>
