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
import { type ipamservicev1_Vlan } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamVlanStore } from '../../stores/ipam-vlan.state';

import VlanDrawer from './vlan-drawer.vue';

const vlanStore = useIpamVlanStore();

const statusOptions = computed(() => [
  { value: 'VLAN_STATUS_ACTIVE', label: $t('ipam.enum.vlanStatus.active') },
  { value: 'VLAN_STATUS_INACTIVE', label: $t('ipam.enum.vlanStatus.inactive') },
  { value: 'VLAN_STATUS_DEPRECATED', label: $t('ipam.enum.vlanStatus.deprecated') },
]);

function statusToColor(status: string | undefined) {
  switch (status) {
    case 'VLAN_STATUS_ACTIVE':
      return '#52C41A';
    case 'VLAN_STATUS_INACTIVE':
      return '#8C8C8C';
    case 'VLAN_STATUS_DEPRECATED':
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
        placeholder: $t('ui.placeholder.input'),
        allowClear: true,
      },
    },
    {
      component: 'Select',
      fieldName: 'status',
      label: $t('ipam.page.vlan.status'),
      componentProps: {
        options: statusOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
  ],
};

const gridOptions: VxeGridProps<ipamservicev1_Vlan> = {
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
        const resp = await vlanStore.listVlans(
          { page: page.currentPage, pageSize: page.pageSize },
          {
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
      title: $t('ipam.page.vlan.vlanId'),
      field: 'vlanId',
      width: 100,
      slots: { default: 'vlanId' },
    },
    { title: $t('ipam.page.vlan.name'), field: 'name', minWidth: 150 },
    {
      title: $t('ipam.page.vlan.status'),
      field: 'status',
      width: 120,
      slots: { default: 'status' },
    },
    { title: $t('ipam.page.vlan.description'), field: 'description', minWidth: 200 },
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

const [VlanDrawerComponent, vlanDrawerApi] = useVbenDrawer({
  connectedComponent: VlanDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function openDrawer(row: ipamservicev1_Vlan, mode: 'create' | 'edit' | 'view') {
  vlanDrawerApi.setData({ row, mode });
  vlanDrawerApi.open();
}

function handleView(row: ipamservicev1_Vlan) {
  openDrawer(row, 'view');
}

function handleEdit(row: ipamservicev1_Vlan) {
  openDrawer(row, 'edit');
}

function handleCreate() {
  openDrawer({} as ipamservicev1_Vlan, 'create');
}

async function handleDelete(row: ipamservicev1_Vlan) {
  if (!row.id) return;
  try {
    await vlanStore.deleteVlan(row.id);
    notification.success({ message: $t('ipam.page.vlan.deleteSuccess') });
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('ipam.page.vlan.title')">
      <template #toolbar-tools>
        <Button class="mr-2" type="primary" @click="handleCreate">
          {{ $t('ipam.page.vlan.create') }}
        </Button>
      </template>
      <template #vlanId="{ row }">
        <span class="font-mono">{{ row.vlanId }}</span>
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
            :title="$t('ipam.page.vlan.confirmDelete')"
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

    <VlanDrawerComponent />
  </Page>
</template>
