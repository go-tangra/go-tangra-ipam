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
  Tabs,
  TabPane,
} from 'ant-design-vue';

import { type ipamservicev1_Device } from '../../api/proto-types';
import { DeviceService, type DeviceInterface } from '../../api/services';
import { WardenService, type WardenSecretRef } from '../../api/warden';
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
  { value: 'DEVICE_STATUS_AVAILABLE', label: $t('ipam.enum.deviceStatus.available') },
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
  ipmiSecretRef?: string;
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
  ipmiSecretRef: undefined,
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
    case 'DEVICE_STATUS_AVAILABLE':
      return '#1890FF';
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
          ipmiSecretRef: formState.value.ipmiSecretRef || undefined,
        },
      );
      notification.success({
        message: $t('ipam.page.device.createSuccess'),
      });
    } else if (isEditMode.value && data.value?.row?.id) {
      const updateMask = ['name', 'description', 'deviceType', 'status', 'locationId', 'serialNumber', 'manufacturer', 'model', 'managementIp', 'ipmiSecretRef'];
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
          ipmiSecretRef: formState.value.ipmiSecretRef || undefined,
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
    ipmiSecretRef: undefined,
  };
  ipmiSecretName.value = '';
  wardenSecretOptions.value = [];
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

      deviceInterfaces.value = [];
      remoteDeviceNames.value = {};

      if (data.value?.mode === 'create') {
        resetForm();
        rackDevices.value = [];
      } else if (data.value?.row) {
        // Load SNMP-discovered ports/links for view mode.
        if (data.value.mode === 'view' && data.value.row.id) {
          await loadDeviceInterfaces(data.value.row.id);
        }
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
          ipmiSecretRef: data.value.row.ipmiSecretRef || undefined,
        };

        // Resolve the linked IPMI secret's display name (view + edit).
        void resolveIpmiSecret(data.value.row.ipmiSecretRef || undefined);

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

// Persisted (SNMP-discovered) interfaces — distinct from the agent-reported
// interfaces embedded in device metadata. These carry the switch-port ifIndex
// and the discovered layer-2 link to a switch.
const deviceInterfaces = ref<DeviceInterface[]>([]);
const loadingInterfaces = ref(false);
const remoteDeviceNames = ref<Record<string, string>>({});

const isSwitchDevice = computed(
  () => device.value?.deviceType === 'DEVICE_TYPE_SWITCH',
);
const hasDiscoveredInterfaces = computed(() => deviceInterfaces.value.length > 0);
const hasDiscoveredLinks = computed(() =>
  deviceInterfaces.value.some((i) => !!i.remotePortName || !!i.remoteInterfaceId),
);

// --- IPMI Warden secret reference (a pointer to a secret, never the value) ---
const wardenSecretOptions = ref<{ value: string; label: string }[]>([]);
const loadingSecrets = ref(false);
const ipmiSecretName = ref(''); // resolved name for view mode
let secretSearchTimer: ReturnType<typeof setTimeout> | undefined;

function secretLabel(s: WardenSecretRef): string {
  const path = s.folderPath && s.folderPath !== '/' ? `${s.folderPath} · ` : '';
  return `${path}${s.name ?? s.id ?? ''}`;
}

function searchWardenSecrets(term: string): void {
  if (secretSearchTimer) clearTimeout(secretSearchTimer);
  secretSearchTimer = setTimeout(async () => {
    loadingSecrets.value = true;
    try {
      const secrets = await WardenService.searchSecrets(term || undefined);
      wardenSecretOptions.value = secrets
        .filter((s): s is WardenSecretRef & { id: string } => !!s.id)
        .map((s) => ({ value: s.id, label: secretLabel(s) }));
    } catch (error: unknown) {
      console.error('Failed to search Warden secrets:', error);
    } finally {
      loadingSecrets.value = false;
    }
  }, 300);
}

// Resolve a secret id to a display name (and seed the picker option so an
// already-linked secret shows its name in edit mode).
async function resolveIpmiSecret(id: string | undefined): Promise<void> {
  ipmiSecretName.value = '';
  if (!id) return;
  try {
    const secret = await WardenService.getSecret(id);
    const label = secret ? secretLabel(secret) : id;
    ipmiSecretName.value = label;
    if (!wardenSecretOptions.value.some((o) => o.value === id)) {
      wardenSecretOptions.value = [{ value: id, label }, ...wardenSecretOptions.value];
    }
  } catch {
    ipmiSecretName.value = id; // fall back to the raw reference
  }
}

// Platform-admin gate for IPMI console access. Mirrors the backend
// (roles platform:admin / super:admin); the backend is the real guard.
const isPlatformAdmin = computed(() => {
  const store = userStore as unknown as {
    userRoles?: string[];
    userInfo?: { roles?: Array<string | { value?: string }> };
  };
  const raw = store.userRoles ?? store.userInfo?.roles ?? [];
  const roles = raw.map((r) => (typeof r === 'string' ? r : (r?.value ?? '')));
  return roles.includes('platform:admin') || roles.includes('super:admin');
});

// Open the BMC KVM console (IPMI View) in a new tab. The page mints the session
// (platform-admin checked server-side) and embeds the console.
function openIpmiConsole(): void {
  const id = device.value?.id;
  if (!id) return;
  window.open(`${window.location.origin}/#/ipam/ipmi-view/${id}`, '_blank', 'noopener');
}

// Switch port the BMC is connected to, discovered by SNMP from the IPMI MAC.
// The link lands on the synthetic "ipmi" interface row.
const ipmiLink = computed(() => {
  const row = deviceInterfaces.value.find(
    (i) => i.name === 'ipmi' && (!!i.remotePortName || !!i.remoteInterfaceId),
  );
  return row ? connectedToLabel(row) : '';
});

async function loadDeviceInterfaces(deviceId: string): Promise<void> {
  loadingInterfaces.value = true;
  try {
    const resp = await DeviceService.getInterfaces(deviceId);
    deviceInterfaces.value = resp.interfaces ?? [];
    await resolveRemoteDeviceNames();
  } catch (error: unknown) {
    console.error('Failed to load device interfaces:', error);
    deviceInterfaces.value = [];
  } finally {
    loadingInterfaces.value = false;
  }
}

// Resolve neighbor (switch) device names for the "Connected To" column.
async function resolveRemoteDeviceNames(): Promise<void> {
  const ids = [
    ...new Set(
      deviceInterfaces.value
        .map((i) => i.remoteDeviceId)
        .filter((v): v is string => !!v),
    ),
  ];
  const names: Record<string, string> = { ...remoteDeviceNames.value };
  await Promise.all(
    ids.map(async (id) => {
      if (names[id]) return;
      try {
        const resp = await DeviceService.get(id);
        if (resp.device?.name) names[id] = resp.device.name;
      } catch {
        // Best effort — fall back to showing the port name only.
      }
    }),
  );
  remoteDeviceNames.value = names;
}

function formatSpeed(speedMbps: number | undefined): string {
  if (!speedMbps || speedMbps <= 0) return '-';
  if (speedMbps >= 1000 && speedMbps % 1000 === 0) {
    return `${speedMbps / 1000} Gbps`;
  }
  return `${speedMbps} Mbps`;
}

// Short host label (first DNS label): "cs1.infra.verax.net" -> "cs1".
function shortHost(name: string): string {
  return name.split('.')[0] || name;
}

// Some switches report a verbose ifDescr that prefixes the chassis model, e.g.
// "X670G2-48x-4q Port 29". Keep the meaningful "Port 29" so the port number —
// the part that matters — is always visible.
function shortPortName(port: string): string {
  const m = port.match(/\bPort\s+.+$/i);
  return m ? m[0] : port;
}

function connectedToLabel(row: DeviceInterface): string {
  if (!row.remotePortName && !row.remoteInterfaceId) return '';
  const switchName = row.remoteDeviceId
    ? remoteDeviceNames.value[row.remoteDeviceId]
    : undefined;
  const port = shortPortName(row.remotePortName ?? row.remoteInterfaceId ?? '');
  return switchName ? `${shortHost(switchName)} · ${port}` : port;
}

// Adaptive, slim columns so the table fits the drawer without horizontal
// scroll. Status is conveyed by row colour (see discoveredRowClass), not a
// column. Switch ports show ifIndex + speed; servers show their NIC + link.
const discoveredInterfaceColumns = computed(() => {
  const cols: Record<string, unknown>[] = [];
  if (isSwitchDevice.value) {
    cols.push({ title: $t('ipam.page.device.portIfIndex'), dataIndex: 'ifIndex', key: 'ifIndex', width: 60 });
  }
  cols.push({ title: $t('ipam.page.device.name'), dataIndex: 'name', key: 'name', ellipsis: true });
  cols.push({ title: $t('ipam.page.device.macAddress'), dataIndex: 'macAddress', key: 'macAddress', width: 150 });
  if (isSwitchDevice.value) {
    cols.push({ title: $t('ipam.page.device.portSpeed'), dataIndex: 'speedMbps', key: 'speedMbps', width: 80 });
  }
  // Discovered links live on the server side; show the column when present.
  // No ellipsis — the port must always be readable, even if it wraps.
  if (hasDiscoveredLinks.value) {
    cols.push({ title: $t('ipam.page.device.connectedTo'), key: 'connectedTo', width: 200 });
  }
  return cols;
});

// Down interfaces are dimmed via row colour instead of a Status column.
function discoveredRowClass(record: DeviceInterface): string {
  return record.enabled === false ? 'iface-row-down' : '';
}

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
  ipmi?: { ip?: string; mac?: string; gateway?: string; subnet?: string };
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

// Agent-reported interfaces embedded in device metadata (distinct from the
// persisted, SNMP-discovered interface rows).
const hasReportedInterfaces = computed(
  () => (parsedMetadata.value?.interfaces?.length ?? 0) > 0,
);

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
  <Drawer :title="title" :footer="false" class="w-full max-w-[860px]">
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

      <!-- IPMI / BMC management interface -->
      <template v-if="parsedMetadata?.ipmi?.ip || parsedMetadata?.ipmi?.mac || device.ipmiSecretRef">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionIpmi') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem v-if="device.ipmiSecretRef" :label="$t('ipam.page.device.ipmiSecret')">
            <Tag color="gold">🔑 {{ ipmiSecretName || device.ipmiSecretRef }}</Tag>
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata?.ipmi?.ip" :label="$t('ipam.page.device.ipmiIp')">
            <Tag color="purple">{{ parsedMetadata?.ipmi?.ip }}</Tag>
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata?.ipmi?.mac" :label="$t('ipam.page.device.ipmiMac')">
            <code>{{ parsedMetadata?.ipmi?.mac }}</code>
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata?.ipmi?.gateway" :label="$t('ipam.page.device.ipmiGateway')">
            {{ parsedMetadata?.ipmi?.gateway }}
          </DescriptionsItem>
          <DescriptionsItem v-if="parsedMetadata?.ipmi?.subnet" :label="$t('ipam.page.device.ipmiSubnet')">
            {{ parsedMetadata?.ipmi?.subnet }}
          </DescriptionsItem>
          <DescriptionsItem v-if="ipmiLink" :label="$t('ipam.page.device.connectedTo')">
            <Tag color="blue" style="white-space: normal; height: auto;">{{ ipmiLink }}</Tag>
          </DescriptionsItem>
          <DescriptionsItem
            v-if="device.ipmiSecretRef && isPlatformAdmin"
            :label="$t('ipam.page.device.ipmiConsole')"
          >
            <Button type="primary" size="small" @click="openIpmiConsole">
              {{ $t('ipam.page.device.ipmiConnect') }}
            </Button>
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

      </template>

      <!-- Interfaces: agent-reported (metadata) + SNMP-discovered ports/links,
           in tabs to avoid a long, wide, horizontally-scrolling stack. -->
      <template v-if="hasReportedInterfaces || hasDiscoveredInterfaces">
        <Divider orientation="left">{{ $t('ipam.page.device.sectionInterfaces') }}</Divider>
        <Tabs size="small">
          <TabPane
            v-if="hasReportedInterfaces"
            key="reported"
            :tab="$t('ipam.page.device.interfaces')"
          >
            <Table
              :columns="interfaceColumns"
              :data-source="parsedMetadata?.interfaces ?? []"
              :pagination="false"
              size="small"
              bordered
              :row-key="(r: any) => r.name"
            />
          </TabPane>
          <TabPane
            v-if="hasDiscoveredInterfaces"
            key="discovered"
            :tab="isSwitchDevice ? $t('ipam.page.device.sectionPorts') : $t('ipam.page.device.sectionDiscoveredInterfaces')"
          >
            <p style="margin: 0 0 8px; color: #8c8c8c; font-size: 12px;">
              {{ $t('ipam.page.device.discoveredInterfacesHint') }}
            </p>
            <Table
              :columns="discoveredInterfaceColumns"
              :data-source="deviceInterfaces"
              :loading="loadingInterfaces"
              :pagination="false"
              size="small"
              bordered
              :row-key="(r: DeviceInterface) => r.id ?? r.name ?? ''"
              :row-class-name="discoveredRowClass"
            >
              <template #bodyCell="{ column, record }">
                <template v-if="column.key === 'ifIndex'">
                  {{ (record as DeviceInterface).ifIndex ?? '-' }}
                </template>
                <template v-else-if="column.key === 'macAddress'">
                  <code v-if="(record as DeviceInterface).macAddress">{{ (record as DeviceInterface).macAddress }}</code>
                  <span v-else>-</span>
                </template>
                <template v-else-if="column.key === 'speedMbps'">
                  {{ formatSpeed((record as DeviceInterface).speedMbps) }}
                </template>
                <template v-else-if="column.key === 'connectedTo'">
                  <template v-if="connectedToLabel(record as DeviceInterface)">
                    <Tag color="blue" style="white-space: normal; height: auto; margin: 0;">
                      {{ connectedToLabel(record as DeviceInterface) }}
                    </Tag>
                    <Tag v-if="(record as DeviceInterface).linkVlan" color="geekblue">
                      {{ $t('ipam.page.device.linkVlan') }} {{ (record as DeviceInterface).linkVlan }}
                    </Tag>
                  </template>
                  <span v-else>-</span>
                </template>
              </template>
            </Table>
          </TabPane>
        </Tabs>
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

        <FormItem :label="$t('ipam.page.device.ipmiSecret')" name="ipmiSecretRef">
          <Select
            v-model:value="formState.ipmiSecretRef"
            :options="wardenSecretOptions"
            :loading="loadingSecrets"
            show-search
            allow-clear
            :filter-option="false"
            :placeholder="$t('ipam.page.device.ipmiSecretPlaceholder')"
            @search="searchWardenSecrets"
            @focus="() => { if (!wardenSecretOptions.length) searchWardenSecrets(''); }"
          />
          <div style="margin-top: 4px; color: #8c8c8c; font-size: 12px;">
            {{ $t('ipam.page.device.ipmiSecretHint') }}
          </div>
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

<style>
/* Disabled (link-down) discovered interfaces are dimmed instead of carrying a
   Status column. Opacity keeps the cue working in both light and dark themes. */
.iface-row-down > td {
  opacity: 0.4;
}
</style>
