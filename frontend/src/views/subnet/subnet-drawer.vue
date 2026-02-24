<script lang="ts" setup>
import { ref, computed, watch } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { useUserStore } from 'shell/vben/stores';
import { LucidePlus, LucideTrash } from 'shell/vben/icons';

import {
  Form,
  FormItem,
  Input,
  InputPassword,
  Button,
  notification,
  Textarea,
  TreeSelect,
  Select,
  SelectOption,
  Descriptions,
  DescriptionsItem,
  Divider,
  Progress,
  Checkbox,
  Alert,
  Collapse,
  CollapsePanel,
  Slider,
  Tag,
  InputNumber,
  Tooltip,
  Space,
  Tabs,
  TabPane,
} from 'ant-design-vue';

import {
  type ipamservicev1_Subnet,
  type ipamservicev1_SubnetTreeNode,
} from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamSubnetStore } from '../../stores/ipam-subnet.state';
import { useIpamVlanStore } from '../../stores/ipam-vlan.state';
import { useIpamLocationStore } from '../../stores/ipam-location.state';
import {
  parseCIDR,
  isIPv6,
  getAvailableSubnets,
  prefixToHosts,
  formatAddressCount,
  isValidChildCidr,
  cidrsOverlap,
  generateSubnetMap,
  vlsmOptimize,
  intToIP,
  type SubnetMapSegment,
  type VLSMRequirement,
  type VLSMAllocation,
} from '../../utils/cidr';

const subnetStore = useIpamSubnetStore();
const vlanStore = useIpamVlanStore();
const locationStore = useIpamLocationStore();
const userStore = useUserStore();

const data = ref<{
  mode: 'create' | 'edit' | 'view';
  parentId?: string;
  subnet?: ipamservicev1_Subnet;
}>();
const loading = ref(false);
const subnetTree = ref<ipamservicev1_SubnetTreeNode[]>([]);
const vlans = ref<Array<{ value: string; label: string }>>([]);
const locations = ref<Array<{ value: string; label: string }>>([]);
const utilization = ref<{ used: number; total: number; percentage: number } | null>(null);

// Calculator state
const calculatorActiveKey = ref<string[]>([]);
const calculatorTab = ref<string>('simple');
const targetPrefix = ref<number>(25);
const selectedSuggestions = ref<Set<string>>(new Set());
const batchCreating = ref(false);

// VLSM state
const vlsmRequirements = ref<VLSMRequirement[]>([]);
const vlsmResult = ref<{
  allocations: VLSMAllocation[];
  unallocated: VLSMRequirement[];
  totalWasted: number;
} | null>(null);

const formState = ref<{
  name: string;
  cidr: string;
  gateway: string;
  dnsServers: string;
  description: string;
  parentId?: string;
  vlanId?: string;
  locationId?: string;
  autoScan: boolean;
  snmpVersion: number;
  snmpCommunity: string;
  snmpUser: string;
  snmpAuthPassword: string;
  snmpPrivPassword: string;
  snmpAuthProtocol: string;
  snmpPrivProtocol: string;
}>({
  name: '',
  cidr: '',
  gateway: '',
  dnsServers: '',
  description: '',
  parentId: undefined,
  vlanId: undefined,
  locationId: undefined,
  autoScan: false,
  snmpVersion: 0,
  snmpCommunity: '',
  snmpUser: '',
  snmpAuthPassword: '',
  snmpPrivPassword: '',
  snmpAuthProtocol: 'SHA',
  snmpPrivProtocol: 'AES',
});

const title = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.subnet.create');
    case 'edit':
      return $t('ipam.page.subnet.edit');
    default:
      return $t('ipam.page.subnet.view');
  }
});

const isCreateMode = computed(() => data.value?.mode === 'create');
const isEditMode = computed(() => data.value?.mode === 'edit');
const isViewMode = computed(() => data.value?.mode === 'view');

// Convert subnet tree to TreeSelect format
interface TreeSelectNode {
  value: string;
  title: string;
  children?: TreeSelectNode[];
  disabled?: boolean;
}

function convertToTreeSelectData(
  nodes: ipamservicev1_SubnetTreeNode[],
  excludeId?: string,
): TreeSelectNode[] {
  return nodes
    .filter((node) => node.subnet?.id !== excludeId)
    .map((node) => ({
      value: node.subnet?.id ?? '',
      title: `${node.subnet?.name || ''} (${node.subnet?.cidr})`,
      children: node.children
        ? convertToTreeSelectData(node.children, excludeId)
        : undefined,
    }));
}

const treeSelectData = computed(() => {
  const excludeId = isEditMode.value ? data.value?.subnet?.id : undefined;
  return convertToTreeSelectData(subnetTree.value, excludeId);
});

// Find a subnet node by ID in the tree
function findSubnetNode(
  nodes: ipamservicev1_SubnetTreeNode[],
  id: string,
): ipamservicev1_SubnetTreeNode | null {
  for (const node of nodes) {
    if (node.subnet?.id === id) return node;
    if (node.children) {
      const found = findSubnetNode(node.children, id);
      if (found) return found;
    }
  }
  return null;
}

// Get parent subnet info
const parentSubnetNode = computed(() => {
  if (!formState.value.parentId) return null;
  return findSubnetNode(subnetTree.value, formState.value.parentId);
});

const parentCidr = computed(() => parentSubnetNode.value?.subnet?.cidr ?? null);

const parentParsed = computed(() => {
  if (!parentCidr.value) return null;
  return parseCIDR(parentCidr.value);
});

// Check if parent is IPv6
const isParentIPv6 = computed(() => {
  if (!parentCidr.value) return false;
  return isIPv6(parentCidr.value);
});

// Get existing children CIDRs of the selected parent
const existingChildrenCidrs = computed(() => {
  if (!parentSubnetNode.value?.children) return [];
  return parentSubnetNode.value.children
    .map((c) => c.subnet?.cidr)
    .filter((c): c is string => !!c);
});

// Get existing children with names for map display
const existingChildrenWithNames = computed(() => {
  if (!parentSubnetNode.value?.children) return [];
  return parentSubnetNode.value.children
    .filter((c) => c.subnet?.cidr)
    .map((c) => ({
      cidr: c.subnet!.cidr!,
      name: c.subnet?.name,
    }));
});

// Available prefix range for calculator
const prefixRange = computed(() => {
  if (!parentParsed.value) return { min: 1, max: 30 };
  return {
    min: parentParsed.value.prefix + 1,
    max: 30,
  };
});

// Show calculator when in create mode and parent is selected
const showCalculator = computed(
  () => isCreateMode.value && formState.value.parentId && !isParentIPv6.value,
);

// Get available subnets at current target prefix
const availableSubnets = computed(() => {
  if (!parentCidr.value || !parentParsed.value) return [];
  return getAvailableSubnets(parentCidr.value, existingChildrenCidrs.value, targetPrefix.value);
});

// Quick division options (show reasonable options based on parent size)
const quickDivisions = computed(() => {
  if (!parentParsed.value) return [];
  const divisions: Array<{ prefix: number; count: number; label: string }> = [];
  const startPrefix = parentParsed.value.prefix + 1;
  const maxDivisions = 5;

  for (let i = 0; i < maxDivisions && startPrefix + i <= 30; i++) {
    const prefix = startPrefix + i;
    const count = Math.pow(2, i + 1);
    const hosts = prefixToHosts(prefix);
    divisions.push({
      prefix,
      count,
      label: `${count}x/${prefix} (${formatAddressCount(hosts)} ${$t('ipam.page.subnet.hostsPerSubnet')})`,
    });
  }

  return divisions;
});

// Generate subnet map segments
const subnetMapSegments = computed((): SubnetMapSegment[] => {
  if (!parentCidr.value) return [];
  const selectedCidrs = Array.from(selectedSuggestions.value);
  return generateSubnetMap(parentCidr.value, existingChildrenWithNames.value, selectedCidrs);
});

// Validate manual CIDR entry against parent
const cidrValidation = computed(() => {
  if (!formState.value.cidr || !parentCidr.value) {
    return { valid: true, message: '' };
  }

  // Check if within parent
  if (!isValidChildCidr(parentCidr.value, formState.value.cidr)) {
    return { valid: false, message: $t('ipam.page.subnet.cidrOutsideParent') };
  }

  // Check for overlaps with siblings
  for (const existing of existingChildrenCidrs.value) {
    if (cidrsOverlap(formState.value.cidr, existing)) {
      return { valid: false, message: $t('ipam.page.subnet.cidrOverlaps') };
    }
  }

  return { valid: true, message: '' };
});

// Watch parent selection to reset calculator state
watch(
  () => formState.value.parentId,
  (newVal) => {
    if (newVal && parentParsed.value) {
      // Set default target prefix to one more than parent
      targetPrefix.value = Math.min(parentParsed.value.prefix + 1, 30);
      selectedSuggestions.value.clear();
      vlsmResult.value = null;
      vlsmRequirements.value = [];
    }
  },
);

// Select a suggested CIDR
function selectSuggestion(cidr: string) {
  formState.value.cidr = cidr;
}

// Toggle suggestion for batch creation
function toggleBatchSelection(cidr: string) {
  if (selectedSuggestions.value.has(cidr)) {
    selectedSuggestions.value.delete(cidr);
  } else {
    selectedSuggestions.value.add(cidr);
  }
  // Trigger reactivity
  selectedSuggestions.value = new Set(selectedSuggestions.value);
}

// Set target prefix from quick division
function setQuickDivision(prefix: number) {
  targetPrefix.value = prefix;
}

// VLSM Functions
function addVlsmRequirement() {
  vlsmRequirements.value.push({
    name: `Subnet ${vlsmRequirements.value.length + 1}`,
    hostsNeeded: 10,
  });
}

function removeVlsmRequirement(index: number) {
  vlsmRequirements.value.splice(index, 1);
  // Recalculate if we have results
  if (vlsmResult.value) {
    calculateVlsm();
  }
}

function calculateVlsm() {
  if (!parentCidr.value || vlsmRequirements.value.length === 0) {
    vlsmResult.value = null;
    return;
  }

  vlsmResult.value = vlsmOptimize(
    parentCidr.value,
    vlsmRequirements.value,
    existingChildrenCidrs.value,
  );
}

function selectVlsmAllocation(allocation: VLSMAllocation) {
  formState.value.cidr = allocation.cidr;
  formState.value.name = allocation.name;
}

// Batch creation
async function createBatchSubnets() {
  if (selectedSuggestions.value.size === 0) return;

  batchCreating.value = true;
  let successCount = 0;
  let failCount = 0;

  try {
    const cidrs = Array.from(selectedSuggestions.value);

    for (const cidr of cidrs) {
      try {
        await subnetStore.createSubnet(
          userStore.tenantId as number,
          {
            name: `${parentSubnetNode.value?.subnet?.name || 'Subnet'} - ${cidr}`,
            cidr,
            parentId: formState.value.parentId,
            vlanId: formState.value.vlanId,
            locationId: formState.value.locationId,
          },
          false,
        );
        successCount++;
      } catch {
        failCount++;
      }
    }

    if (successCount > 0) {
      notification.success({
        message: $t('ipam.page.subnet.batchCreateSuccess', { count: successCount }),
      });
    }
    if (failCount > 0) {
      notification.warning({
        message: $t('ipam.page.subnet.batchCreatePartial', { success: successCount, fail: failCount }),
      });
    }

    if (successCount > 0) {
      drawerApi.close();
    }
  } catch (e) {
    console.error('Batch creation failed:', e);
    notification.error({
      message: $t('ui.notification.create_failed'),
    });
  } finally {
    batchCreating.value = false;
  }
}

// Get color for map segment
function getSegmentColor(type: 'used' | 'available' | 'suggested'): string {
  switch (type) {
    case 'used':
      return '#ff4d4f';
    case 'suggested':
      return '#1890ff';
    case 'available':
      return '#52c41a';
    default:
      return '#d9d9d9';
  }
}

async function loadSubnetTree() {
  try {
    const resp = await subnetStore.getSubnetTree();
    subnetTree.value = (resp.nodes ?? []) as ipamservicev1_SubnetTreeNode[];
  } catch (e) {
    console.error('Failed to load subnet tree:', e);
  }
}

async function loadVlans() {
  try {
    const resp = await vlanStore.listVlans({ page: 1, pageSize: 100 });
    vlans.value = (resp.items ?? []).map((v) => ({
      value: v.id ?? '',
      label: `${v.vlanId} - ${v.name}`,
    }));
  } catch (e) {
    console.error('Failed to load VLANs:', e);
  }
}

async function loadLocations() {
  try {
    const resp = await locationStore.listLocations({ page: 1, pageSize: 100 });
    locations.value = (resp.items ?? []).map((l) => ({
      value: l.id ?? '',
      label: l.name ?? '',
    }));
  } catch (e) {
    console.error('Failed to load locations:', e);
  }
}

async function loadUtilization(subnetId: string) {
  try {
    const resp = await subnetStore.getSubnetStats(subnetId);
    utilization.value = {
      used: Number(resp.usedAddresses ?? 0),
      total: Number(resp.totalAddresses ?? 0),
      percentage: Number(resp.utilization ?? 0),
    };
  } catch (e) {
    console.error('Failed to load utilization:', e);
    utilization.value = null;
  }
}

function formatDateTime(value: string | undefined) {
  if (!value) return '-';
  try {
    return new Date(value).toLocaleString();
  } catch {
    return value;
  }
}

async function handleSubmit() {
  loading.value = true;
  try {
    if (isCreateMode.value) {
      await subnetStore.createSubnet(
        userStore.tenantId as number,
        {
          name: formState.value.name,
          cidr: formState.value.cidr,
          gateway: formState.value.gateway || undefined,
          dnsServers: formState.value.dnsServers || undefined,
          description: formState.value.description || undefined,
          parentId: formState.value.parentId,
          vlanId: formState.value.vlanId,
          locationId: formState.value.locationId,
          snmpVersion: formState.value.snmpVersion || undefined,
          snmpCommunity: formState.value.snmpCommunity || undefined,
          snmpUser: formState.value.snmpUser || undefined,
          snmpAuthPassword: formState.value.snmpAuthPassword || undefined,
          snmpPrivPassword: formState.value.snmpPrivPassword || undefined,
          snmpAuthProtocol: formState.value.snmpVersion === 3 ? formState.value.snmpAuthProtocol : undefined,
          snmpPrivProtocol: formState.value.snmpVersion === 3 ? formState.value.snmpPrivProtocol : undefined,
        },
        formState.value.autoScan,
      );
      notification.success({
        message: $t('ipam.page.subnet.createSuccess'),
      });
    } else if (isEditMode.value && data.value?.subnet?.id) {
      await subnetStore.updateSubnet(
        data.value.subnet.id,
        {
          name: formState.value.name,
          gateway: formState.value.gateway || undefined,
          dnsServers: formState.value.dnsServers || undefined,
          description: formState.value.description || undefined,
          vlanId: formState.value.vlanId,
          locationId: formState.value.locationId,
          snmpVersion: formState.value.snmpVersion,
          snmpCommunity: formState.value.snmpCommunity || undefined,
          snmpUser: formState.value.snmpUser || undefined,
          snmpAuthPassword: formState.value.snmpAuthPassword || undefined,
          snmpPrivPassword: formState.value.snmpPrivPassword || undefined,
          snmpAuthProtocol: formState.value.snmpVersion === 3 ? formState.value.snmpAuthProtocol : undefined,
          snmpPrivProtocol: formState.value.snmpVersion === 3 ? formState.value.snmpPrivProtocol : undefined,
        },
        ['name', 'gateway', 'dnsServers', 'description', 'vlanId', 'locationId', 'snmpVersion', 'snmpCommunity', 'snmpUser', 'snmpAuthPassword', 'snmpPrivPassword', 'snmpAuthProtocol', 'snmpPrivProtocol'],
      );
      notification.success({
        message: $t('ipam.page.subnet.updateSuccess'),
      });
    }
    drawerApi.close();
  } catch (e) {
    console.error('Failed to save subnet:', e);
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
    cidr: '',
    gateway: '',
    dnsServers: '',
    description: '',
    parentId: undefined,
    vlanId: undefined,
    locationId: undefined,
    autoScan: false,
    snmpVersion: 0,
    snmpCommunity: '',
    snmpUser: '',
    snmpAuthPassword: '',
    snmpPrivPassword: '',
    snmpAuthProtocol: 'SHA',
    snmpPrivProtocol: 'AES',
  };
  utilization.value = null;
}

const [Drawer, drawerApi] = useVbenDrawer({
  onCancel() {
    drawerApi.close();
  },

  async onOpenChange(isOpen) {
    if (isOpen) {
      data.value = drawerApi.getData() as {
        mode: 'create' | 'edit' | 'view';
        parentId?: string;
        subnet?: ipamservicev1_Subnet;
      };

      await Promise.all([loadSubnetTree(), loadVlans(), loadLocations()]);

      if (data.value?.mode === 'create') {
        resetForm();
        formState.value.parentId = data.value.parentId;
      } else if (data.value?.subnet) {
        formState.value = {
          name: data.value.subnet.name ?? '',
          cidr: data.value.subnet.cidr ?? '',
          gateway: data.value.subnet.gateway ?? '',
          dnsServers: data.value.subnet.dnsServers ?? '',
          description: data.value.subnet.description ?? '',
          parentId: data.value.subnet.parentId,
          vlanId: data.value.subnet.vlanId,
          locationId: data.value.subnet.locationId,
          autoScan: false,
          snmpVersion: data.value.subnet.snmpVersion ?? 0,
          snmpCommunity: data.value.subnet.snmpCommunity ?? '',
          snmpUser: data.value.subnet.snmpUser ?? '',
          snmpAuthPassword: data.value.subnet.snmpAuthPassword ?? '',
          snmpPrivPassword: data.value.subnet.snmpPrivPassword ?? '',
          snmpAuthProtocol: data.value.subnet.snmpAuthProtocol ?? 'SHA',
          snmpPrivProtocol: data.value.subnet.snmpPrivProtocol ?? 'AES',
        };
        if (data.value.subnet.id) {
          await loadUtilization(data.value.subnet.id);
        }
      }
    }
  },
});

const subnet = computed(() => data.value?.subnet);

// Calculated network info for view mode
const networkInfo = computed(() => {
  if (!subnet.value?.cidr) return null;

  const parsed = parseCIDR(subnet.value.cidr);
  if (!parsed) return null;

  const isPointToPoint = parsed.prefix >= 31;

  return {
    networkAddress: intToIP(parsed.networkInt),
    broadcastAddress: intToIP(parsed.broadcastInt),
    firstHost: isPointToPoint ? intToIP(parsed.networkInt) : intToIP(parsed.networkInt + 1),
    lastHost: isPointToPoint ? intToIP(parsed.broadcastInt) : intToIP(parsed.broadcastInt - 1),
    usableHosts: parsed.usableHosts,
    totalAddresses: parsed.totalAddresses,
    prefixLength: parsed.prefix,
    subnetMask: intToIP(~((1 << (32 - parsed.prefix)) - 1) >>> 0),
    isIPv4: true,
    wildcardMask: intToIP(((1 << (32 - parsed.prefix)) - 1) >>> 0),
  };
});
</script>

<template>
  <Drawer :title="title" :footer="false">
    <!-- View Mode -->
    <template v-if="subnet && isViewMode">
      <!-- Basic Info -->
      <Descriptions :column="1" bordered size="small">
        <DescriptionsItem :label="$t('ipam.page.subnet.name')">
          {{ subnet.name || '-' }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.subnet.cidr')">
          <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ subnet.cidr }}</code>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.subnet.gateway')">
          <code v-if="subnet.gateway" class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ subnet.gateway }}</code>
          <span v-else class="text-muted-foreground">-</span>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.subnet.dnsServers')">
          <code v-if="subnet.dnsServers" class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ subnet.dnsServers }}</code>
          <span v-else class="text-muted-foreground">-</span>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.subnet.description')">
          {{ subnet.description || '-' }}
        </DescriptionsItem>
      </Descriptions>

      <!-- SNMP Configuration -->
      <template v-if="subnet.snmpVersion && subnet.snmpVersion > 0">
        <Divider>{{ $t('ipam.page.subnet.snmpConfig') }}</Divider>
        <Descriptions :column="1" bordered size="small">
          <DescriptionsItem :label="$t('ipam.page.subnet.snmpVersion')">
            {{ subnet.snmpVersion === 2 ? $t('ipam.page.subnet.snmpV2c') : $t('ipam.page.subnet.snmpV3') }}
          </DescriptionsItem>
          <DescriptionsItem v-if="subnet.snmpVersion === 2" :label="$t('ipam.page.subnet.snmpCommunity')">
            {{ subnet.snmpCommunity || '-' }}
          </DescriptionsItem>
          <template v-if="subnet.snmpVersion === 3">
            <DescriptionsItem :label="$t('ipam.page.subnet.snmpUser')">
              {{ subnet.snmpUser || '-' }}
            </DescriptionsItem>
            <DescriptionsItem :label="$t('ipam.page.subnet.snmpAuthPassword')">
              ********
            </DescriptionsItem>
            <DescriptionsItem :label="$t('ipam.page.subnet.snmpPrivPassword')">
              ********
            </DescriptionsItem>
            <DescriptionsItem :label="$t('ipam.page.subnet.snmpAuthProtocol')">
              {{ subnet.snmpAuthProtocol || '-' }}
            </DescriptionsItem>
            <DescriptionsItem :label="$t('ipam.page.subnet.snmpPrivProtocol')">
              {{ subnet.snmpPrivProtocol || '-' }}
            </DescriptionsItem>
          </template>
        </Descriptions>
      </template>

      <!-- Network Details -->
      <template v-if="networkInfo">
        <Divider>{{ $t('ipam.page.subnet.networkDetails') }}</Divider>
        <Descriptions :column="2" bordered size="small">
          <DescriptionsItem :label="$t('ipam.page.subnet.networkAddress')">
            <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ networkInfo.networkAddress }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.broadcastAddress')">
            <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ networkInfo.broadcastAddress }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.firstHost')">
            <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ networkInfo.firstHost }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.lastHost')">
            <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ networkInfo.lastHost }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.subnetMask')">
            <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ networkInfo.subnetMask }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.wildcardMask')">
            <code class="bg-muted rounded px-1.5 py-0.5 font-mono">{{ networkInfo.wildcardMask }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.totalAddresses')">
            {{ formatAddressCount(networkInfo.totalAddresses) }}
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.usableHosts')">
            {{ formatAddressCount(networkInfo.usableHosts) }}
          </DescriptionsItem>
        </Descriptions>
      </template>

      <!-- Utilization -->
      <template v-if="utilization">
        <Divider>{{ $t('ipam.page.subnet.utilization') }}</Divider>
        <div class="mb-4">
          <Progress
            :percent="utilization.percentage"
            :stroke-color="utilization.percentage > 80 ? '#ff4d4f' : utilization.percentage > 60 ? '#faad14' : '#52c41a'"
          />
        </div>
        <Descriptions :column="2" bordered size="small">
          <DescriptionsItem :label="$t('ipam.page.subnet.usedAddresses')">
            {{ utilization.used.toLocaleString() }}
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.subnet.availableAddresses')">
            {{ (utilization.total - utilization.used).toLocaleString() }}
          </DescriptionsItem>
        </Descriptions>
      </template>

      <Divider>{{ $t('ui.table.timestamps') }}</Divider>
      <Descriptions :column="2" bordered size="small">
        <DescriptionsItem :label="$t('ui.table.createdAt')">
          {{ formatDateTime(subnet.createdAt) }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ui.table.updatedAt')">
          {{ formatDateTime(subnet.updatedAt) }}
        </DescriptionsItem>
      </Descriptions>
    </template>

    <!-- Create/Edit Mode -->
    <template v-else-if="isCreateMode || isEditMode">
      <Form layout="vertical" :model="formState" @finish="handleSubmit">
        <FormItem
          :label="$t('ipam.page.subnet.name')"
          name="name"
        >
          <Input
            v-model:value="formState.name"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem
          v-if="isCreateMode"
          :label="$t('ipam.page.subnet.cidr')"
          name="cidr"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Input
            v-model:value="formState.cidr"
            placeholder="192.168.1.0/24"
            :maxlength="43"
          />
        </FormItem>

        <FormItem
          v-if="isCreateMode"
          :label="$t('ipam.page.subnet.parent')"
          name="parentId"
        >
          <TreeSelect
            v-model:value="formState.parentId"
            :tree-data="treeSelectData"
            :placeholder="$t('ipam.page.subnet.rootSubnet')"
            allow-clear
            tree-default-expand-all
          />
        </FormItem>

        <!-- Subnet Calculator Section -->
        <template v-if="showCalculator && parentParsed">
          <Divider class="!my-3">
            {{ $t('ipam.page.subnet.calculator') }}
          </Divider>

          <!-- Parent Info -->
          <Alert type="info" class="mb-4" show-icon>
            <template #message>
              <div class="flex items-center justify-between">
                <span>
                  {{ $t('ipam.page.subnet.parent') }}: <code>{{ parentCidr }}</code>
                </span>
                <span class="text-muted-foreground">
                  {{ formatAddressCount(parentParsed.totalAddresses) }} {{ $t('ipam.page.subnet.totalAddresses').toLowerCase() }}
                </span>
              </div>
            </template>
          </Alert>

          <!-- IPv6 Warning -->
          <Alert
            v-if="isParentIPv6"
            type="warning"
            :message="$t('ipam.page.subnet.ipv6NotSupported')"
            show-icon
            class="mb-4"
          />

          <!-- Calculator Tabs -->
          <Collapse v-model:activeKey="calculatorActiveKey">
            <CollapsePanel key="calculator" :header="$t('ipam.page.subnet.calculatorHelp')">
              <Tabs v-model:activeKey="calculatorTab" size="small">
                <!-- Simple Calculator Tab -->
                <TabPane key="simple" :tab="$t('ipam.page.subnet.simpleMode')">
                  <!-- Quick Divisions -->
                  <div class="mb-4">
                    <div class="text-muted-foreground mb-2 text-sm">{{ $t('ipam.page.subnet.quickDivisions') }}</div>
                    <Space wrap>
                      <Button
                        v-for="div in quickDivisions"
                        :key="div.prefix"
                        size="small"
                        :type="targetPrefix === div.prefix ? 'primary' : 'default'"
                        @click="setQuickDivision(div.prefix)"
                      >
                        {{ div.label }}
                      </Button>
                    </Space>
                  </div>

                  <!-- Target Prefix Slider -->
                  <div class="mb-4">
                    <div class="text-muted-foreground mb-2 text-sm">
                      {{ $t('ipam.page.subnet.targetPrefix') }}: /{{ targetPrefix }}
                      ({{ formatAddressCount(prefixToHosts(targetPrefix)) }} {{ $t('ipam.page.subnet.hostsPerSubnet') }})
                    </div>
                    <Slider
                      v-model:value="targetPrefix"
                      :min="prefixRange.min"
                      :max="prefixRange.max"
                      :marks="{
                        [prefixRange.min]: `/${prefixRange.min}`,
                        [prefixRange.max]: `/${prefixRange.max}`,
                      }"
                    />
                  </div>

                  <!-- Available Subnets -->
                  <div class="mb-4">
                    <div class="text-muted-foreground mb-2 text-sm">
                      {{ $t('ipam.page.subnet.availableSubnets') }}
                      <Tag v-if="availableSubnets.length > 0" color="green">
                        {{ availableSubnets.length }}
                      </Tag>
                    </div>

                    <Alert
                      v-if="availableSubnets.length === 0"
                      type="warning"
                      :message="$t('ipam.page.subnet.noAvailableSubnets')"
                      show-icon
                    />

                    <div v-else class="border-border max-h-48 overflow-y-auto rounded border">
                      <div
                        v-for="subnet in availableSubnets"
                        :key="subnet.cidr"
                        class="border-border flex items-center justify-between border-b px-3 py-2 last:border-b-0 hover:bg-accent"
                      >
                        <div class="flex items-center gap-2">
                          <Checkbox
                            :checked="selectedSuggestions.has(subnet.cidr)"
                            @change="toggleBatchSelection(subnet.cidr)"
                          />
                          <code class="bg-muted rounded px-1.5 py-0.5 text-sm">{{ subnet.cidr }}</code>
                          <span class="text-muted-foreground text-xs">
                            ({{ formatAddressCount(subnet.usableHosts) }} {{ $t('ipam.page.subnet.hostsPerSubnet') }})
                          </span>
                        </div>
                        <Button
                          type="link"
                          size="small"
                          @click="selectSuggestion(subnet.cidr)"
                        >
                          {{ $t('ipam.page.subnet.selectCidr') }}
                        </Button>
                      </div>
                    </div>
                  </div>

                  <!-- Batch Create Button -->
                  <div v-if="selectedSuggestions.size > 0" class="mb-4">
                    <Button
                      type="primary"
                      :loading="batchCreating"
                      @click="createBatchSubnets"
                    >
                      <LucidePlus class="size-4" />
                      {{ $t('ipam.page.subnet.batchCreate', { count: selectedSuggestions.size }) }}
                    </Button>
                  </div>
                </TabPane>

                <!-- VLSM Tab -->
                <TabPane key="vlsm" :tab="$t('ipam.page.subnet.vlsmMode')">
                  <div class="mb-4">
                    <div class="text-muted-foreground mb-2 text-sm">{{ $t('ipam.page.subnet.vlsmHelp') }}</div>

                    <!-- Requirements List -->
                    <div class="mb-4 space-y-2">
                      <div
                        v-for="(req, index) in vlsmRequirements"
                        :key="index"
                        class="flex items-center gap-2"
                      >
                        <Input
                          v-model:value="req.name"
                          :placeholder="$t('ipam.page.subnet.name')"
                          style="width: 150px"
                        />
                        <InputNumber
                          v-model:value="req.hostsNeeded"
                          :min="1"
                          :max="prefixToHosts(prefixRange.min)"
                          :placeholder="$t('ipam.page.subnet.hostsNeeded')"
                          style="width: 120px"
                        />
                        <span class="text-muted-foreground text-xs">{{ $t('ipam.page.subnet.hostsPerSubnet') }}</span>
                        <Button
                          type="text"
                          danger
                          size="small"
                          @click="removeVlsmRequirement(index)"
                        >
                          <LucideTrash class="size-4" />
                        </Button>
                      </div>
                    </div>

                    <Space>
                      <Button size="small" @click="addVlsmRequirement">
                        <LucidePlus class="size-4" />
                        {{ $t('ipam.page.subnet.addRequirement') }}
                      </Button>
                      <Button
                        type="primary"
                        size="small"
                        :disabled="vlsmRequirements.length === 0"
                        @click="calculateVlsm"
                      >
                        {{ $t('ipam.page.subnet.optimizeVlsm') }}
                      </Button>
                    </Space>
                  </div>

                  <!-- VLSM Results -->
                  <template v-if="vlsmResult">
                    <Divider class="!my-3" />

                    <div v-if="vlsmResult.allocations.length > 0" class="mb-4">
                      <div class="mb-2 text-sm font-medium">{{ $t('ipam.page.subnet.vlsmAllocations') }}</div>
                      <div class="border-border max-h-48 overflow-y-auto rounded border">
                        <div
                          v-for="alloc in vlsmResult.allocations"
                          :key="alloc.cidr"
                          class="border-border flex items-center justify-between border-b px-3 py-2 last:border-b-0 hover:bg-accent"
                        >
                          <div>
                            <div class="font-medium">{{ alloc.name }}</div>
                            <code class="bg-muted rounded px-1.5 py-0.5 text-sm">{{ alloc.cidr }}</code>
                            <span class="text-muted-foreground ml-2 text-xs">
                              {{ formatAddressCount(alloc.usableHosts) }}/{{ alloc.hostsNeeded }} {{ $t('ipam.page.subnet.hostsPerSubnet') }}
                              <span v-if="alloc.wastedAddresses > 0" style="color: #f97316">
                                (+{{ alloc.wastedAddresses }} {{ $t('ipam.page.subnet.wasted') }})
                              </span>
                            </span>
                          </div>
                          <Button
                            type="link"
                            size="small"
                            @click="selectVlsmAllocation(alloc)"
                          >
                            {{ $t('ipam.page.subnet.selectCidr') }}
                          </Button>
                        </div>
                      </div>
                      <div class="text-muted-foreground mt-2 text-xs">
                        {{ $t('ipam.page.subnet.totalWasted') }}: {{ vlsmResult.totalWasted }} {{ $t('ipam.page.subnet.addresses') }}
                      </div>
                    </div>

                    <Alert
                      v-if="vlsmResult.unallocated.length > 0"
                      type="warning"
                      show-icon
                      class="mb-4"
                    >
                      <template #message>
                        {{ $t('ipam.page.subnet.vlsmUnallocated') }}:
                        {{ vlsmResult.unallocated.map(u => u.name).join(', ') }}
                      </template>
                    </Alert>
                  </template>
                </TabPane>

                <!-- Visual Map Tab -->
                <TabPane key="map" :tab="$t('ipam.page.subnet.visualMap')">
                  <div class="mb-4">
                    <div class="text-muted-foreground mb-2 text-sm">{{ $t('ipam.page.subnet.mapHelp') }}</div>

                    <!-- Legend -->
                    <div class="mb-3 flex gap-4 text-xs">
                      <span class="flex items-center gap-1">
                        <span class="h-3 w-3 rounded" :style="{ backgroundColor: getSegmentColor('used') }"></span>
                        {{ $t('ipam.page.subnet.mapUsed') }}
                      </span>
                      <span class="flex items-center gap-1">
                        <span class="h-3 w-3 rounded" :style="{ backgroundColor: getSegmentColor('available') }"></span>
                        {{ $t('ipam.page.subnet.mapAvailable') }}
                      </span>
                      <span class="flex items-center gap-1">
                        <span class="h-3 w-3 rounded" :style="{ backgroundColor: getSegmentColor('suggested') }"></span>
                        {{ $t('ipam.page.subnet.mapSelected') }}
                      </span>
                    </div>

                    <!-- Map Bar -->
                    <div class="border-border flex h-8 overflow-hidden rounded border">
                      <Tooltip
                        v-for="(segment, index) in subnetMapSegments"
                        :key="index"
                        :title="`${segment.cidr || `${segment.startAddress} - ${segment.endAddress}`} (${segment.percentage.toFixed(1)}%)`"
                      >
                        <div
                          :style="{
                            width: `${Math.max(segment.percentage, 0.5)}%`,
                            backgroundColor: getSegmentColor(segment.type),
                            minWidth: '2px',
                          }"
                          class="h-full cursor-pointer transition-all hover:opacity-80"
                        ></div>
                      </Tooltip>
                    </div>

                    <!-- Segment List -->
                    <div class="mt-3 max-h-32 overflow-y-auto text-xs">
                      <div
                        v-for="(segment, index) in subnetMapSegments"
                        :key="index"
                        class="border-border flex items-center justify-between border-b py-1 last:border-b-0"
                      >
                        <div class="flex items-center gap-2">
                          <span
                            class="h-2 w-2 rounded"
                            :style="{ backgroundColor: getSegmentColor(segment.type) }"
                          ></span>
                          <code class="bg-muted rounded px-1 py-0.5">{{ segment.cidr || `${segment.startAddress} - ${segment.endAddress}` }}</code>
                          <span v-if="segment.name" class="text-muted-foreground">({{ segment.name }})</span>
                        </div>
                        <span class="text-muted-foreground">{{ segment.percentage.toFixed(1) }}%</span>
                      </div>
                    </div>
                  </div>
                </TabPane>
              </Tabs>
            </CollapsePanel>
          </Collapse>

          <!-- CIDR Validation Warning -->
          <Alert
            v-if="formState.cidr && !cidrValidation.valid"
            type="error"
            :message="cidrValidation.message"
            show-icon
            class="mt-4"
          />
        </template>

        <FormItem :label="$t('ipam.page.subnet.gateway')" name="gateway">
          <Input
            v-model:value="formState.gateway"
            placeholder="192.168.1.1"
            :maxlength="39"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.subnet.dnsServers')" name="dnsServers">
          <Input
            v-model:value="formState.dnsServers"
            :placeholder="$t('ipam.page.subnet.dnsServersPlaceholder')"
            :maxlength="255"
          />
          <div class="text-xs text-muted-foreground mt-1">
            {{ $t('ipam.page.subnet.dnsServersHelp') }}
          </div>
        </FormItem>

        <FormItem :label="$t('ipam.page.vlan.title')" name="vlanId">
          <Select
            v-model:value="formState.vlanId"
            :options="vlans"
            :placeholder="$t('ui.placeholder.select')"
            allow-clear
            show-search
            :filter-option="(input: string, option: any) =>
              option.label.toLowerCase().includes(input.toLowerCase())"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.location.title')" name="locationId">
          <Select
            v-model:value="formState.locationId"
            :options="locations"
            :placeholder="$t('ui.placeholder.select')"
            allow-clear
            show-search
            :filter-option="(input: string, option: any) =>
              option.label.toLowerCase().includes(input.toLowerCase())"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.subnet.description')" name="description">
          <Textarea
            v-model:value="formState.description"
            :rows="3"
            :maxlength="1024"
            :placeholder="$t('ui.placeholder.input')"
          />
        </FormItem>

        <!-- SNMP Configuration -->
        <Divider class="!my-3">{{ $t('ipam.page.subnet.snmpConfig') }}</Divider>

        <FormItem :label="$t('ipam.page.subnet.snmpVersion')" name="snmpVersion">
          <Select v-model:value="formState.snmpVersion">
            <SelectOption :value="0">{{ $t('ipam.page.subnet.snmpNone') }}</SelectOption>
            <SelectOption :value="2">{{ $t('ipam.page.subnet.snmpV2c') }}</SelectOption>
            <SelectOption :value="3">{{ $t('ipam.page.subnet.snmpV3') }}</SelectOption>
          </Select>
        </FormItem>

        <FormItem
          v-if="formState.snmpVersion === 2"
          :label="$t('ipam.page.subnet.snmpCommunity')"
          name="snmpCommunity"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Input
            v-model:value="formState.snmpCommunity"
            :placeholder="$t('ipam.page.subnet.snmpCommunityPlaceholder')"
            :maxlength="255"
          />
        </FormItem>

        <template v-if="formState.snmpVersion === 3">
          <FormItem
            :label="$t('ipam.page.subnet.snmpUser')"
            name="snmpUser"
            :rules="[{ required: true, message: $t('ui.formRules.required') }]"
          >
            <Input
              v-model:value="formState.snmpUser"
              :placeholder="$t('ui.placeholder.input')"
              :maxlength="255"
            />
          </FormItem>

          <FormItem :label="$t('ipam.page.subnet.snmpAuthPassword')" name="snmpAuthPassword">
            <InputPassword
              v-model:value="formState.snmpAuthPassword"
              :placeholder="$t('ui.placeholder.input')"
              :maxlength="255"
            />
          </FormItem>

          <FormItem :label="$t('ipam.page.subnet.snmpPrivPassword')" name="snmpPrivPassword">
            <InputPassword
              v-model:value="formState.snmpPrivPassword"
              :placeholder="$t('ui.placeholder.input')"
              :maxlength="255"
            />
          </FormItem>

          <FormItem :label="$t('ipam.page.subnet.snmpAuthProtocol')" name="snmpAuthProtocol">
            <Select v-model:value="formState.snmpAuthProtocol">
              <SelectOption value="MD5">MD5</SelectOption>
              <SelectOption value="SHA">SHA</SelectOption>
            </Select>
          </FormItem>

          <FormItem :label="$t('ipam.page.subnet.snmpPrivProtocol')" name="snmpPrivProtocol">
            <Select v-model:value="formState.snmpPrivProtocol">
              <SelectOption value="DES">DES</SelectOption>
              <SelectOption value="AES">AES</SelectOption>
            </Select>
          </FormItem>
        </template>

        <FormItem v-if="isCreateMode" name="autoScan">
          <Checkbox v-model:checked="formState.autoScan">
            {{ $t('ipam.page.subnet.autoScan') }}
          </Checkbox>
          <Alert
            v-if="formState.autoScan"
            type="info"
            show-icon
            class="mt-2"
            :message="$t('ipam.page.subnet.autoScanHint')"
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
