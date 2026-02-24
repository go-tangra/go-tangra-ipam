<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, computed } from 'vue';

import { Page, useVbenDrawer, type VbenFormProps } from 'shell/vben/common-ui';
import {
  LucideEye,
  LucideTrash,
  LucidePencil,
} from 'shell/vben/icons';

import { notification, Space, Button, Tag } from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { type ipamservicev1_IpAddress } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamIpAddressStore } from '../../stores/ipam-ip-address.state';

import IpAddressDrawer from './ip-address-drawer.vue';

const ipAddressStore = useIpamIpAddressStore();

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
        placeholder: $t('ipam.page.ipAddress.searchPlaceholder'),
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

  sortConfig: {
    remote: true,
    defaultSort: { field: 'address', order: 'asc' },
  },

  proxyConfig: {
    sort: true,
    ajax: {
      query: async ({ page, sorts }, formValues) => {
        // Build orderBy from VxeGrid sort state
        const orderBy: string[] = [];
        if (sorts && sorts.length > 0) {
          for (const sort of sorts) {
            if (sort.field && sort.order) {
              orderBy.push(sort.order === 'desc' ? `-${sort.field}` : sort.field);
            }
          }
        }

        const resp = await ipAddressStore.listIpAddresses(
          { page: page.currentPage, pageSize: page.pageSize },
          {
            query: formValues?.query,
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
      title: $t('ipam.page.ipAddress.address'),
      field: 'address',
      minWidth: 150,
      sortable: true,
      slots: { default: 'address' },
    },
    { title: $t('ipam.page.ipAddress.hostname'), field: 'hostname', width: 150, sortable: true },
    {
      title: $t('ipam.page.ipAddress.status'),
      field: 'status',
      width: 120,
      sortable: true,
      slots: { default: 'status' },
    },
    { title: $t('ipam.page.ipAddress.macAddress'), field: 'macAddress', width: 150, sortable: true },
    { title: $t('ipam.page.ipAddress.description'), field: 'description', minWidth: 200, sortable: true },
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

const [IpAddressDrawerComponent, ipAddressDrawerApi] = useVbenDrawer({
  connectedComponent: IpAddressDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function openDrawer(row: ipamservicev1_IpAddress, mode: 'create' | 'edit' | 'view') {
  ipAddressDrawerApi.setData({ row, mode });
  ipAddressDrawerApi.open();
}

function handleView(row: ipamservicev1_IpAddress) {
  openDrawer(row, 'view');
}

function handleEdit(row: ipamservicev1_IpAddress) {
  openDrawer(row, 'edit');
}

function handleCreate() {
  openDrawer({} as ipamservicev1_IpAddress, 'create');
}

async function handleDelete(row: ipamservicev1_IpAddress) {
  if (!row.id) return;
  try {
    await ipAddressStore.deleteIpAddress(row.id);
    notification.success({ message: $t('ipam.page.ipAddress.deleteSuccess') });
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('ipam.page.ipAddress.title')">
      <template #toolbar-tools>
        <Button class="mr-2" type="primary" @click="handleCreate">
          {{ $t('ipam.page.ipAddress.create') }}
        </Button>
      </template>
      <template #address="{ row }">
        <span class="font-mono">{{ row.address }}</span>
      </template>
      <template #status="{ row }">
        <Tag :color="statusToColor(row.status)">
          {{ statusToName(row.status) }}
        </Tag>
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
            :title="$t('ipam.page.ipAddress.confirmDelete')"
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

    <IpAddressDrawerComponent />
  </Page>
</template>
