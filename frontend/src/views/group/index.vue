<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, computed } from 'vue';

import { Page, useVbenDrawer, type VbenFormProps } from 'shell/vben/common-ui';
import { LucideEye, LucideTrash, LucidePencil } from 'shell/vben/icons';

import { notification, Space, Button, Tag } from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { type ipamservicev1_IpGroup } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamGroupStore } from '../../stores/ipam-group.state';

import GroupDrawer from './group-drawer.vue';

const groupStore = useIpamGroupStore();

const statusOptions = computed(() => [
  { value: 'IP_GROUP_STATUS_ACTIVE', label: $t('ipam.enum.groupStatus.active') },
  { value: 'IP_GROUP_STATUS_INACTIVE', label: $t('ipam.enum.groupStatus.inactive') },
]);

function statusToColor(status: string | undefined) {
  switch (status) {
    case 'IP_GROUP_STATUS_ACTIVE':
      return '#52C41A';
    case 'IP_GROUP_STATUS_INACTIVE':
      return '#8C8C8C';
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
      label: $t('ipam.page.group.status'),
      componentProps: {
        options: statusOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
  ],
};

const gridOptions: VxeGridProps<ipamservicev1_IpGroup> = {
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
        const resp = await groupStore.listGroups(
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
      title: $t('ipam.page.group.name'),
      field: 'name',
      minWidth: 200,
    },
    { title: $t('ipam.page.group.description'), field: 'description', minWidth: 250 },
    {
      title: $t('ipam.page.group.memberCount'),
      field: 'memberCount',
      width: 120,
      align: 'center',
    },
    {
      title: $t('ipam.page.group.status'),
      field: 'status',
      width: 120,
      slots: { default: 'status' },
    },
    {
      title: $t('ui.table.action'),
      field: 'action',
      width: 150,
      fixed: 'right',
      slots: { default: 'action' },
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [GroupDrawerComponent, groupDrawerApi] = useVbenDrawer({
  connectedComponent: GroupDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      gridApi.query();
    }
  },
});

function handleAdd() {
  groupDrawerApi.setData({ row: {}, mode: 'create' });
  groupDrawerApi.open();
}

function handleView(row: ipamservicev1_IpGroup) {
  groupDrawerApi.setData({ row, mode: 'view' });
  groupDrawerApi.open();
}

function handleEdit(row: ipamservicev1_IpGroup) {
  groupDrawerApi.setData({ row, mode: 'edit' });
  groupDrawerApi.open();
}

async function handleDelete(row: ipamservicev1_IpGroup) {
  if (!row.id) return;
  try {
    await groupStore.deleteGroup(row.id);
    notification.success({ message: $t('ipam.page.group.deleteSuccess') });
    gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}
</script>

<template>
  <Page auto-content-height>
    <Grid :table-title="$t('ipam.page.group.title')">
      <template #toolbar-tools>
        <Button type="primary"  @click="handleAdd">
          {{ $t('ipam.page.group.create') }}
        </Button>
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
            @click="handleView(row)"
          />
          <Button
            type="link"
            size="small"
            :icon="h(LucidePencil)"
            @click="handleEdit(row)"
          />
          <a-popconfirm
            :cancel-text="$t('ui.button.cancel')"
            :ok-text="$t('ui.button.ok')"
            :title="$t('ipam.page.group.confirmDelete')"
            @confirm="handleDelete(row)"
          >
            <Button danger type="link" size="small" :icon="h(LucideTrash)" />
          </a-popconfirm>
        </Space>
      </template>
    </Grid>

    <GroupDrawerComponent />
  </Page>
</template>
