<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, ref, computed, onMounted } from 'vue';

import { Page, useVbenDrawer, type VbenFormProps } from 'shell/vben/common-ui';
import { useUserStore } from 'shell/vben/stores';
import {
  LucideEye,
  LucideTrash,
  LucidePencil,
  LucidePlus,
  LucideRefreshCw,
  LucideXCircle,
  LucideCheckCircle,
} from 'shell/vben/icons';

import { notification, Space, Button, Tree, Card, Empty, Tag, Modal, Progress, Tooltip, Descriptions, DescriptionsItem, Divider, Checkbox } from 'ant-design-vue';
import type { TreeProps } from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import {
  type ipamservicev1_IpAddress,
  type ipamservicev1_SubnetTreeNode,
  type ipamservicev1_IpScanJob,
} from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamSubnetStore } from '../../stores/ipam-subnet.state';
import { useIpamIpAddressStore } from '../../stores/ipam-ip-address.state';
import { useIpamIpScanStore } from '../../stores/ipam-ip-scan.state';

import SubnetDrawer from './subnet-drawer.vue';

const subnetStore = useIpamSubnetStore();
const ipAddressStore = useIpamIpAddressStore();
const ipScanStore = useIpamIpScanStore();
const userStore = useUserStore();

const treeData = ref<TreeProps['treeData']>([]);
const selectedSubnetId = ref<string | undefined>(undefined);
const selectedSubnetName = ref<string>('');
const treeLoading = ref(false);
const expandedKeys = ref<(string | number)[]>([]);

// Scan job state
const scanningSubnetIds = ref<Set<string>>(new Set());
const scanModalVisible = ref(false);
const currentScanJob = ref<ipamservicev1_IpScanJob | null>(null);
const scanPollingTimer = ref<ReturnType<typeof setInterval> | null>(null);

interface TreeNode {
  key: string;
  title: string;
  children?: TreeNode[];
  subnet: ipamservicev1_SubnetTreeNode['subnet'];
}

function buildTreeData(nodes: ipamservicev1_SubnetTreeNode[]): TreeNode[] {
  return nodes.map((node) => ({
    key: node.subnet?.id ?? '',
    title: `${node.subnet?.cidr ?? ''} ${node.subnet?.name ? `- ${node.subnet.name}` : ''}`,
    children: node.children ? buildTreeData(node.children) : undefined,
    subnet: node.subnet,
  }));
}

async function loadSubnetTree() {
  treeLoading.value = true;
  try {
    const resp = await subnetStore.getSubnetTree(undefined, 10);
    treeData.value = buildTreeData((resp.nodes ?? []) as ipamservicev1_SubnetTreeNode[]);
    if (treeData.value.length > 0) {
      expandedKeys.value = treeData.value.map((n) => n.key);
    }
  } catch (e) {
    console.error('Failed to load subnet tree:', e);
  } finally {
    treeLoading.value = false;
  }
}

function handleTreeSelect(
  keys: (string | number)[],
  info: { node: any },
) {
  if (keys.length > 0) {
    selectedSubnetId.value = String(keys[0]);
    selectedSubnetName.value = info.node.subnet?.name || info.node.subnet?.cidr || '';
    gridApi.reload();
  } else {
    selectedSubnetId.value = undefined;
    selectedSubnetName.value = '';
  }
}

const statusOptions = computed(() => [
  { value: 'IP_ADDRESS_STATUS_ACTIVE', label: $t('ipam.enum.addressStatus.active') },
  { value: 'IP_ADDRESS_STATUS_RESERVED', label: $t('ipam.enum.addressStatus.reserved') },
  { value: 'IP_ADDRESS_STATUS_DHCP', label: $t('ipam.enum.addressStatus.dhcp') },
  { value: 'IP_ADDRESS_STATUS_DEPRECATED', label: $t('ipam.enum.addressStatus.deprecated') },
  { value: 'IP_ADDRESS_STATUS_OFFLINE', label: $t('ipam.enum.addressStatus.offline') },
]);

function statusToColor(status: string | undefined) {
  switch (status) {
    case 'IP_ADDRESS_STATUS_ACTIVE':
      return '#52C41A';
    case 'IP_ADDRESS_STATUS_RESERVED':
      return '#FAAD14';
    case 'IP_ADDRESS_STATUS_DHCP':
      return '#1890FF';
    case 'IP_ADDRESS_STATUS_DEPRECATED':
      return '#8C8C8C';
    case 'IP_ADDRESS_STATUS_OFFLINE':
      return '#FF4D4F';
    default:
      return '#C9CDD4';
  }
}

function statusToName(status: string | undefined) {
  const option = statusOptions.value.find((o) => o.value === status);
  return option?.label ?? status ?? '';
}

// IP Detail Modal State
const ipDetailModalVisible = ref(false);
const selectedIpAddress = ref<ipamservicev1_IpAddress | null>(null);

function handleViewIpDetail(row: ipamservicev1_IpAddress) {
  selectedIpAddress.value = row;
  ipDetailModalVisible.value = true;
}

function closeIpDetailModal() {
  ipDetailModalVisible.value = false;
  selectedIpAddress.value = null;
}

const formOptions: VbenFormProps = {
  collapsed: false,
  showCollapseButton: false,
  submitOnEnter: true,
  schema: [
    {
      component: 'Input',
      fieldName: 'query',
      label: $t('ui.table.search'),
      componentProps: {
        placeholder: $t('ui.placeholder.input'),
        allowClear: true,
      },
    },
    {
      component: 'Select',
      fieldName: 'status',
      label: $t('ipam.page.ipAddress.status'),
      componentProps: {
        options: statusOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
  ],
};

const gridOptions: VxeGridProps<ipamservicev1_IpAddress> = {
  height: 'auto',
  stripe: false,
  toolbarConfig: {
    custom: true,
    export: true,
    import: false,
    refresh: true,
    zoom: true,
  },
  exportConfig: {},
  rowConfig: {
    isHover: true,
  },
  pagerConfig: {
    enabled: true,
    pageSize: 20,
    pageSizes: [10, 20, 50, 100],
  },

  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        if (!selectedSubnetId.value) {
          return { items: [], total: 0 };
        }
        const resp = await ipAddressStore.listIpAddresses(
          { page: page.currentPage, pageSize: page.pageSize },
          {
            subnetId: selectedSubnetId.value,
            query: formValues?.query,
            status: formValues?.status,
          },
        );
        return {
          items: resp.items ?? [],
          total: resp.total ?? 0,
        };
      },
    },
  },

  columns: [
    { title: $t('ui.table.seq'), type: 'seq', width: 50 },
    {
      title: $t('ipam.page.ipAddress.address'),
      field: 'address',
      minWidth: 150,
      slots: { default: 'address' },
    },
    {
      title: $t('ipam.page.ipAddress.hostname'),
      field: 'hostname',
      width: 200,
      slots: { default: 'hostname' },
    },
    {
      title: $t('ipam.page.ipAddress.status'),
      field: 'status',
      width: 120,
      slots: { default: 'status' },
    },
    { title: $t('ipam.page.ipAddress.macAddress'), field: 'macAddress', width: 150 },
    { title: $t('ipam.page.ipAddress.description'), field: 'description', minWidth: 200 },
    {
      title: $t('ui.table.action'),
      field: 'action',
      width: 80,
      fixed: 'right',
      slots: { default: 'action' },
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [SubnetDrawerComponent, subnetDrawerApi] = useVbenDrawer({
  connectedComponent: SubnetDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      loadSubnetTree();
    }
  },
});

function handleCreateSubnet(parentId?: string) {
  subnetDrawerApi.setData({ row: {}, mode: 'create', parentId });
  subnetDrawerApi.open();
}

function handleEditSubnet(subnetId: string) {
  const subnet = findSubnetInTree(treeData.value ?? [], subnetId);
  if (subnet) {
    subnetDrawerApi.setData({ subnet, mode: 'edit' });
    subnetDrawerApi.open();
  }
}

function handleViewSubnet(subnetId: string) {
  const subnet = findSubnetInTree(treeData.value ?? [], subnetId);
  if (subnet) {
    subnetDrawerApi.setData({ subnet, mode: 'view' });
    subnetDrawerApi.open();
  }
}

function findSubnetInTree(nodes: any[], id: string): any | null {
  for (const node of nodes) {
    if (node.key === id) {
      return node.subnet;
    }
    if (node.children) {
      const found = findSubnetInTree(node.children, id);
      if (found) return found;
    }
  }
  return null;
}

async function handleDeleteSubnet(subnetId: string) {
  try {
    await subnetStore.deleteSubnet(subnetId);
    notification.success({ message: $t('ipam.page.subnet.deleteSuccess') });
    await loadSubnetTree();
    if (selectedSubnetId.value === subnetId) {
      selectedSubnetId.value = undefined;
      selectedSubnetName.value = '';
    }
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}

// Scan configuration state
const scanConfigVisible = ref(false);
const scanEnableSnmp = ref(false);
const scanEnableDnsUpdate = ref(false);
const scanTargetSubnetId = ref<string>('');

// Show scan config before starting
function handleStartScan(subnetId: string) {
  scanTargetSubnetId.value = subnetId;
  scanEnableSnmp.value = false;
  scanEnableDnsUpdate.value = false;
  scanConfigVisible.value = true;
}

async function confirmStartScan() {
  const subnetId = scanTargetSubnetId.value;
  scanConfigVisible.value = false;
  try {
    scanningSubnetIds.value.add(subnetId);
    const resp = await ipScanStore.startScan(
      userStore.tenantId as number,
      subnetId,
      undefined,
      scanEnableSnmp.value,
      scanEnableDnsUpdate.value,
    );
    if (resp.job) {
      currentScanJob.value = resp.job as ipamservicev1_IpScanJob;
      scanModalVisible.value = true;
      startScanPolling(resp.job.id!);
    }
    notification.success({ message: $t('ipam.page.subnet.scanStarted') });
  } catch (e: any) {
    notification.error({
      message: $t('ipam.page.subnet.scanFailed'),
      description: e?.message || String(e),
    });
    scanningSubnetIds.value.delete(subnetId);
  }
}

function startScanPolling(jobId: string) {
  if (scanPollingTimer.value) {
    clearInterval(scanPollingTimer.value);
  }
  scanPollingTimer.value = setInterval(async () => {
    try {
      const resp = await ipScanStore.getScanJob(jobId);
      if (resp.job) {
        currentScanJob.value = resp.job as ipamservicev1_IpScanJob;
        // Stop polling when scan is complete
        if (
          resp.job.status === 'IP_SCAN_JOB_STATUS_COMPLETED' ||
          resp.job.status === 'IP_SCAN_JOB_STATUS_FAILED' ||
          resp.job.status === 'IP_SCAN_JOB_STATUS_CANCELLED'
        ) {
          stopScanPolling();
          if (resp.job.subnetId) {
            scanningSubnetIds.value.delete(resp.job.subnetId);
          }
          // Refresh IP address list if this subnet is selected
          if (selectedSubnetId.value === resp.job.subnetId) {
            gridApi.query();
          }
        }
      }
    } catch (e) {
      console.error('Failed to poll scan job:', e);
    }
  }, 2000);
}

function stopScanPolling() {
  if (scanPollingTimer.value) {
    clearInterval(scanPollingTimer.value);
    scanPollingTimer.value = null;
  }
}

async function handleCancelScan() {
  if (currentScanJob.value?.id) {
    try {
      await ipScanStore.cancelScan(currentScanJob.value.id);
      notification.success({ message: $t('ipam.page.subnet.scanCancelled') });
    } catch {
      notification.error({ message: $t('ui.notification.failed') });
    }
  }
}

function closeScanModal() {
  scanModalVisible.value = false;
  stopScanPolling();
}

function scanJobStatusToColor(status: string | undefined) {
  switch (status) {
    case 'IP_SCAN_JOB_STATUS_PENDING':
      return '#FAAD14';
    case 'IP_SCAN_JOB_STATUS_SCANNING':
      return '#1890FF';
    case 'IP_SCAN_JOB_STATUS_COMPLETED':
      return '#52C41A';
    case 'IP_SCAN_JOB_STATUS_FAILED':
      return '#FF4D4F';
    case 'IP_SCAN_JOB_STATUS_CANCELLED':
      return '#8C8C8C';
    default:
      return '#C9CDD4';
  }
}

function scanJobStatusToName(status: string | undefined) {
  switch (status) {
    case 'IP_SCAN_JOB_STATUS_PENDING':
      return $t('ipam.enum.scanJobStatus.pending');
    case 'IP_SCAN_JOB_STATUS_SCANNING':
      return $t('ipam.enum.scanJobStatus.scanning');
    case 'IP_SCAN_JOB_STATUS_COMPLETED':
      return $t('ipam.enum.scanJobStatus.completed');
    case 'IP_SCAN_JOB_STATUS_FAILED':
      return $t('ipam.enum.scanJobStatus.failed');
    case 'IP_SCAN_JOB_STATUS_CANCELLED':
      return $t('ipam.enum.scanJobStatus.cancelled');
    default:
      return status ?? '';
  }
}

onMounted(() => {
  loadSubnetTree();
});
</script>

<template>
  <Page auto-content-height>
    <div class="flex h-full gap-4">
      <!-- Left: Subnet Tree -->
      <Card
        class="w-96 shrink-0"
        :title="$t('ipam.page.subnet.tree')"
        :loading="treeLoading"
      >
        <template #extra>
          <Button type="link" size="small" @click="handleCreateSubnet()">
            <template #icon>
              <component :is="LucidePlus" class="size-4" />
            </template>
          </Button>
        </template>
        <div v-if="treeData && treeData.length > 0">
          <Tree
            v-model:expandedKeys="expandedKeys"
            :tree-data="treeData"
            :selectable="true"
            :default-expand-all="true"
            @select="handleTreeSelect"
          >
            <template #title="{ title, key }">
              <div class="group flex items-center gap-2">
                <span class="font-mono text-sm">{{ title }}</span>
                <Space class="invisible group-hover:visible" size="small">
                  <Tooltip :title="$t('ipam.page.subnet.scan')">
                    <Button
                      type="link"
                      size="small"
                      :loading="scanningSubnetIds.has(key)"
                      :icon="h(LucideRefreshCw, { class: scanningSubnetIds.has(key) ? 'animate-spin' : '' })"
                      @click.stop="handleStartScan(key)"
                    />
                  </Tooltip>
                  <Button
                    type="link"
                    size="small"
                    :icon="h(LucideEye)"
                    @click.stop="handleViewSubnet(key)"
                  />
                  <Button
                    type="link"
                    size="small"
                    :icon="h(LucidePlus)"
                    @click.stop="handleCreateSubnet(key)"
                  />
                  <Button
                    type="link"
                    size="small"
                    :icon="h(LucidePencil)"
                    @click.stop="handleEditSubnet(key)"
                  />
                  <a-popconfirm
                    :cancel-text="$t('ui.button.cancel')"
                    :ok-text="$t('ui.button.ok')"
                    :title="$t('ipam.page.subnet.confirmDelete')"
                    @confirm="handleDeleteSubnet(key)"
                  >
                    <Button
                      danger
                      type="link"
                      size="small"
                      :icon="h(LucideTrash)"
                      @click.stop
                    />
                  </a-popconfirm>
                </Space>
              </div>
            </template>
          </Tree>
        </div>
        <Empty v-else :description="$t('ipam.page.subnet.noSubnets')" />
      </Card>

      <!-- Right: IP Address List -->
      <div class="flex-1">
        <Grid :table-title="selectedSubnetName || $t('ipam.page.ipAddress.title')">
          <template #address="{ row }">
            <a
              class="cursor-pointer font-mono text-blue-600 hover:text-blue-800 hover:underline"
              @click="handleViewIpDetail(row)"
            >
              {{ row.address }}
            </a>
          </template>
          <template #hostname="{ row }">
            <div class="flex items-center gap-2">
              <span>{{ row.hostname || '-' }}</span>
              <Tooltip v-if="row.hasReverseDns === false" :title="$t('ipam.page.ipAddress.noReverseDns')">
                <component :is="LucideXCircle" class="size-4 text-orange-500" />
              </Tooltip>
              <Tooltip v-else-if="row.hasReverseDns === true" :title="$t('ipam.page.ipAddress.hasReverseDns')">
                <component :is="LucideCheckCircle" class="size-4 text-green-500" />
              </Tooltip>
            </div>
          </template>
          <template #status="{ row }">
            <Tag :color="statusToColor(row.status)">
              {{ statusToName(row.status) }}
            </Tag>
          </template>
          <template #action="{ row }">
            <Button type="link" size="small" :icon="h(LucideEye)" @click="handleViewIpDetail(row)" />
          </template>
        </Grid>
      </div>
    </div>

    <SubnetDrawerComponent />

    <!-- IP Address Detail Modal -->
    <Modal
      v-model:open="ipDetailModalVisible"
      :title="$t('ipam.page.ipAddress.view')"
      :footer="null"
      width="600px"
      @cancel="closeIpDetailModal"
    >
      <div v-if="selectedIpAddress" class="space-y-4">
        <Descriptions :column="2" bordered size="small">
          <DescriptionsItem :label="$t('ipam.page.ipAddress.address')" :span="2">
            <code class="font-mono">{{ selectedIpAddress.address }}</code>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.hostname')">
            {{ selectedIpAddress.hostname || '-' }}
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.reverseDns')">
            <Tag v-if="selectedIpAddress.hasReverseDns" color="success">
              {{ $t('ipam.page.ipAddress.hasReverseDns') }}
            </Tag>
            <Tag v-else color="warning">
              {{ $t('ipam.page.ipAddress.noReverseDns') }}
            </Tag>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.status')">
            <Tag :color="statusToColor(selectedIpAddress.status)">
              {{ statusToName(selectedIpAddress.status) }}
            </Tag>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.macAddress')">
            <code v-if="selectedIpAddress.macAddress" class="font-mono">{{ selectedIpAddress.macAddress }}</code>
            <span v-else>-</span>
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.dnsName')">
            {{ selectedIpAddress.dnsName || '-' }}
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.ptrRecord')">
            {{ selectedIpAddress.ptrRecord || '-' }}
          </DescriptionsItem>
          <DescriptionsItem :label="$t('ipam.page.ipAddress.description')" :span="2">
            {{ selectedIpAddress.description || '-' }}
          </DescriptionsItem>
        </Descriptions>

        <Divider v-if="selectedIpAddress.lastSeen">{{ $t('ipam.page.ipAddress.scanInfo') }}</Divider>
        <Descriptions v-if="selectedIpAddress.lastSeen" :column="2" bordered size="small">
          <DescriptionsItem :label="$t('ipam.page.ipAddress.lastSeen')">
            {{ new Date(selectedIpAddress.lastSeen).toLocaleString() }}
          </DescriptionsItem>
        </Descriptions>

        <div class="flex justify-end pt-4">
          <Button @click="closeIpDetailModal">
            {{ $t('ui.button.close') }}
          </Button>
        </div>
      </div>
    </Modal>

    <!-- Scan Configuration Modal -->
    <Modal
      v-model:open="scanConfigVisible"
      :title="$t('ipam.page.subnet.scan')"
      :ok-text="$t('ipam.page.subnet.scan')"
      :cancel-text="$t('ui.button.cancel')"
      @ok="confirmStartScan"
    >
      <div class="space-y-4 py-2">
        <Checkbox v-model:checked="scanEnableSnmp">
          {{ $t('ipam.page.subnet.enableSnmp') }}
        </Checkbox>
        <div v-if="scanEnableSnmp" class="text-muted-foreground ml-6 text-xs">
          {{ $t('ipam.page.subnet.enableSnmpHint') }}
        </div>
        <Checkbox v-model:checked="scanEnableDnsUpdate">
          {{ $t('ipam.page.subnet.enableDnsUpdate') }}
        </Checkbox>
        <div
          v-if="scanEnableDnsUpdate"
          class="text-muted-foreground ml-6 text-xs"
        >
          {{ $t('ipam.page.subnet.enableDnsUpdateHint') }}
        </div>
      </div>
    </Modal>

    <!-- Scan Job Progress Modal -->
    <Modal
      v-model:open="scanModalVisible"
      :title="$t('ipam.page.subnet.scanProgress')"
      :footer="null"
      :mask-closable="false"
      @cancel="closeScanModal"
    >
      <div v-if="currentScanJob" class="space-y-4">
        <div class="flex items-center justify-between">
          <span class="font-medium">{{ $t('ipam.page.subnet.scanStatus') }}</span>
          <Tag :color="scanJobStatusToColor(currentScanJob.status)">
            {{ scanJobStatusToName(currentScanJob.status) }}
          </Tag>
        </div>

        <Progress
          :percent="currentScanJob.progress ?? 0"
          :status="currentScanJob.status === 'IP_SCAN_JOB_STATUS_FAILED' ? 'exception' : currentScanJob.status === 'IP_SCAN_JOB_STATUS_COMPLETED' ? 'success' : 'active'"
        />

        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <span class="text-gray-500">{{ $t('ipam.page.subnet.totalAddresses') }}:</span>
            <span class="ml-2 font-medium">{{ currentScanJob.totalAddresses ?? 0 }}</span>
          </div>
          <div>
            <span class="text-gray-500">{{ $t('ipam.page.subnet.scannedCount') }}:</span>
            <span class="ml-2 font-medium">{{ currentScanJob.scannedCount ?? 0 }}</span>
          </div>
          <div>
            <span class="text-gray-500">{{ $t('ipam.page.subnet.aliveCount') }}:</span>
            <span class="ml-2 font-medium text-green-600">{{ currentScanJob.aliveCount ?? 0 }}</span>
          </div>
          <div>
            <span class="text-gray-500">{{ $t('ipam.page.subnet.newCount') }}:</span>
            <span class="ml-2 font-medium text-blue-600">{{ currentScanJob.newCount ?? 0 }}</span>
          </div>
          <div v-if="currentScanJob.enableSnmp">
            <span class="text-gray-500">{{ $t('ipam.page.subnet.snmpDiscoveredCount') }}:</span>
            <span class="ml-2 font-medium text-purple-600">{{ currentScanJob.snmpDiscoveredCount ?? 0 }}</span>
          </div>
        </div>

        <div v-if="currentScanJob.statusMessage && (currentScanJob.status === 'IP_SCAN_JOB_STATUS_FAILED')" class="rounded bg-red-50 p-3 text-sm text-red-600">
          {{ currentScanJob.statusMessage }}
        </div>

        <div class="flex justify-end gap-2 pt-4">
          <Button
            v-if="currentScanJob.status === 'IP_SCAN_JOB_STATUS_PENDING' || currentScanJob.status === 'IP_SCAN_JOB_STATUS_SCANNING'"
            danger
            @click="handleCancelScan"
          >
            {{ $t('ipam.page.subnet.cancelScan') }}
          </Button>
          <Button @click="closeScanModal">
            {{ $t('ui.button.close') }}
          </Button>
        </div>
      </div>
    </Modal>
  </Page>
</template>
