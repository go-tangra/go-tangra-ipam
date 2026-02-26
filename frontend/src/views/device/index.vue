<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, computed } from 'vue';
import { useRouter } from 'vue-router';

import { Page, useVbenDrawer, type VbenFormProps } from 'shell/vben/common-ui';
import {
  LucideEye,
  LucideTrash,
  LucidePencil,
} from 'shell/vben/icons';

import { notification, Space, Button, Tag } from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { type ipamservicev1_Device } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamDeviceStore } from '../../stores/ipam-device.state';

import DeviceDrawer from './device-drawer.vue';

const router = useRouter();
const deviceStore = useIpamDeviceStore();

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

function deviceTypeToName(deviceType: string | undefined) {
  const option = deviceTypeOptions.value.find((o) => o.value === deviceType);
  return option?.label ?? deviceType ?? '';
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

function isServerOrVM(deviceType: string | undefined) {
  return deviceType === 'DEVICE_TYPE_SERVER' || deviceType === 'DEVICE_TYPE_VM';
}

function navigateToPackages(deviceId: string) {
  router.push({ path: '/ipam/packages', query: { deviceId } });
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
      fieldName: 'deviceType',
      label: $t('ipam.page.device.deviceType'),
      componentProps: {
        options: deviceTypeOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
    {
      component: 'Select',
      fieldName: 'status',
      label: $t('ipam.page.device.status'),
      componentProps: {
        options: statusOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
  ],
};

const gridOptions: VxeGridProps<ipamservicev1_Device> = {
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

  sortConfig: {
    remote: true,
    defaultSort: { field: 'name', order: 'asc' },
  },

  proxyConfig: {
    sort: true,
    ajax: {
      query: async ({ page, sorts }, formValues) => {
        const orderBy: string[] = [];
        if (sorts && sorts.length > 0) {
          for (const sort of sorts) {
            if (sort.field && sort.order) {
              orderBy.push(sort.order === 'desc' ? `-${sort.field}` : sort.field);
            }
          }
        }
        const resp = await deviceStore.listDevices(
          { page: page.currentPage, pageSize: page.pageSize },
          {
            query: formValues?.query,
            deviceType: formValues?.deviceType,
            status: formValues?.status,
          },
          orderBy.length > 0 ? orderBy : undefined,
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
      title: $t('ipam.page.device.name'),
      field: 'name',
      minWidth: 150,
      sortable: true,
      slots: { default: 'name' },
    },
    {
      title: $t('ipam.page.device.deviceType'),
      field: 'deviceType',
      width: 150,
      sortable: true,
      slots: { default: 'deviceType' },
    },
    {
      title: $t('ipam.page.device.status'),
      field: 'status',
      width: 120,
      sortable: true,
      slots: { default: 'status' },
    },
    { title: $t('ipam.page.device.primaryIp'), field: 'primaryIp', width: 140, sortable: true },
    { title: $t('ipam.page.device.managementIp'), field: 'managementIp', width: 140, sortable: true },
    { title: $t('ipam.page.device.osVersion'), field: 'osVersion', width: 150, sortable: true },
    { title: $t('ipam.page.device.description'), field: 'description', minWidth: 150, sortable: true },
    {
      title: $t('ipam.page.device.updateStatus'),
      field: 'packageUpdateCount',
      width: 180,
      slots: { default: 'updateStatus' },
    },
    {
      title: $t('ui.table.action'),
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      width: 150,
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [DeviceDrawerComponent, deviceDrawerApi] = useVbenDrawer({
  connectedComponent: DeviceDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function openDrawer(row: ipamservicev1_Device, mode: 'create' | 'edit' | 'view') {
  deviceDrawerApi.setData({ row, mode });
  deviceDrawerApi.open();
}

function handleView(row: ipamservicev1_Device) {
  openDrawer(row, 'view');
}

function handleEdit(row: ipamservicev1_Device) {
  openDrawer(row, 'edit');
}

function handleCreate() {
  openDrawer({} as ipamservicev1_Device, 'create');
}

async function handleDelete(row: ipamservicev1_Device) {
  if (!row.id) return;
  try {
    await deviceStore.deleteDevice(row.id);
    notification.success({ message: $t('ipam.page.device.deleteSuccess') });
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('ipam.page.device.title')">
      <template #toolbar-tools>
        <Button class="mr-2" type="primary" @click="handleCreate">
          {{ $t('ipam.page.device.create') }}
        </Button>
      </template>
      <template #name="{ row }">
        <span>{{ row.name }}</span>
      </template>
      <template #deviceType="{ row }">
        <Tag :color="deviceTypeToColor(row.deviceType)">
          {{ deviceTypeToName(row.deviceType) }}
        </Tag>
      </template>
      <template #status="{ row }">
        <Tag :color="statusToColor(row.status)">
          {{ statusToName(row.status) }}
        </Tag>
      </template>
      <template #updateStatus="{ row }">
        <template v-if="isServerOrVM(row.deviceType)">
          <span
            v-if="row.packageUpdateCount == null"
            class="text-gray-400"
          >-</span>
          <span
            v-else-if="row.packageUpdateCount === 0"
            class="cursor-pointer"
            @click="navigateToPackages(row.id)"
          >
            <Tag color="success">{{ $t('ipam.page.device.upToDate') }}</Tag>
          </span>
          <span
            v-else
            class="cursor-pointer"
            @click="navigateToPackages(row.id)"
          >
            <Tag color="warning">{{ $t('ipam.page.device.updatesAvailable', { count: row.packageUpdateCount }) }}</Tag>
            <Tag v-if="row.securityUpdateCount > 0" color="error">{{ $t('ipam.page.device.securityUpdates', { count: row.securityUpdateCount }) }}</Tag>
          </span>
        </template>
      </template>
      <template #action="{ row }">
        <Space>
          <Button
            type="link"
            size="small"
            :icon="h(LucideEye)"
            :title="$t('ui.button.view')"
            @click.stop="handleView(row)"
          />
          <Button
            type="link"
            size="small"
            :icon="h(LucidePencil)"
            :title="$t('ui.button.edit')"
            @click.stop="handleEdit(row)"
          />
          <a-popconfirm
            :cancel-text="$t('ui.button.cancel')"
            :ok-text="$t('ui.button.ok')"
            :title="$t('ipam.page.device.confirmDelete')"
            @confirm="handleDelete(row)"
          >
            <Button
              danger
              type="link"
              size="small"
              :icon="h(LucideTrash)"
              :title="$t('ui.button.delete', { moduleName: '' })"
            />
          </a-popconfirm>
        </Space>
      </template>
    </Grid>

    <DeviceDrawerComponent />
  </Page>
</template>
