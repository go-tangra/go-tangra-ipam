<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, ref, computed, watch } from 'vue';
import { useRoute } from 'vue-router';

import { Page, type VbenFormProps } from 'shell/vben/common-ui';
import { LucideTrash } from 'shell/vben/icons';

import { notification, Tag, Space, Button, Card, Statistic, Row, Col } from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import { type ipamservicev1_DevicePackage } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamDevicePackageStore } from '../../stores/ipam-device-package.state';
import { useIpamDeviceStore } from '../../stores/ipam-device.state';
import type { GetDevicePackageStatsResponse } from '../../api/services';

const route = useRoute();
const devicePackageStore = useIpamDevicePackageStore();
const deviceStore = useIpamDeviceStore();

const selectedDeviceId = ref<string>('');
const stats = ref<GetDevicePackageStatsResponse>({});
const deviceOptions = ref<{ value: string; label: string }[]>([]);

// Load devices for the selector
async function loadDevices() {
  try {
    const resp = await deviceStore.listDevices({ page: 1, pageSize: 500 });
    deviceOptions.value = (resp.items ?? []).map((d: any) => ({
      value: d.id!,
      label: `${d.name}${d.primaryIp ? ` (${d.primaryIp})` : ''}`,
    }));
  } catch {
    // ignore
  }
}
loadDevices().then(() => {
  // Pre-select device from query param if present
  const queryDeviceId = route.query.deviceId;
  if (typeof queryDeviceId === 'string' && queryDeviceId) {
    selectedDeviceId.value = queryDeviceId;
    gridApi.query();
  }
});

// Load stats when device changes
watch(selectedDeviceId, async (deviceId) => {
  if (deviceId) {
    try {
      stats.value = await devicePackageStore.getDevicePackageStats(deviceId);
    } catch {
      stats.value = {};
    }
  } else {
    stats.value = {};
  }
});

const packageManagerOptions = computed(() => [
  { value: 'apt', label: 'APT (Debian/Ubuntu)' },
  { value: 'dnf', label: 'DNF (Fedora/RHEL)' },
  { value: 'apk', label: 'APK (Alpine)' },
  { value: 'pacman', label: 'Pacman (Arch)' },
]);

const updateFilterOptions = computed(() => [
  { value: true, label: $t('ipam.page.packages.needsUpdate') },
  { value: false, label: $t('ipam.page.packages.upToDate') },
]);

const securityFilterOptions = computed(() => [
  { value: true, label: $t('ipam.page.packages.securityUpdate') },
  { value: false, label: $t('ipam.page.packages.notSecurity') },
]);

const formOptions: VbenFormProps = {
  collapsed: false,
  showCollapseButton: false,
  submitOnEnter: true,
  schema: [
    {
      component: 'Select',
      fieldName: 'deviceId',
      label: $t('ipam.page.packages.device'),
      componentProps: {
        options: deviceOptions,
        placeholder: $t('ipam.page.packages.selectDevice'),
        allowClear: false,
        showSearch: true,
        filterOption: (input: string, option: any) =>
          option.label.toLowerCase().includes(input.toLowerCase()),
        onChange: (val: string) => {
          selectedDeviceId.value = val;
        },
      },
    },
    {
      component: 'Input',
      fieldName: 'query',
      label: $t('ui.table.search'),
      componentProps: {
        placeholder: $t('ipam.page.packages.searchPlaceholder'),
        allowClear: true,
      },
    },
    {
      component: 'Select',
      fieldName: 'needsUpdate',
      label: $t('ipam.page.packages.updateStatus'),
      componentProps: {
        options: updateFilterOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
    {
      component: 'Select',
      fieldName: 'isSecurityUpdate',
      label: $t('ipam.page.packages.securityFilter'),
      componentProps: {
        options: securityFilterOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
    {
      component: 'Select',
      fieldName: 'packageManager',
      label: $t('ipam.page.packages.packageManager'),
      componentProps: {
        options: packageManagerOptions,
        placeholder: $t('ui.placeholder.select'),
        allowClear: true,
      },
    },
  ],
};

const gridOptions: VxeGridProps<ipamservicev1_DevicePackage> = {
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
    pageSize: 50,
    pageSizes: [20, 50, 100, 200],
  },

  sortConfig: {
    remote: true,
    defaultSort: { field: 'name', order: 'asc' },
  },

  proxyConfig: {
    sort: true,
    ajax: {
      query: async ({ page, sorts }, formValues) => {
        const deviceId = formValues?.deviceId || selectedDeviceId.value;
        if (!deviceId) {
          return { items: [], total: 0 };
        }

        const orderBy: string[] = [];
        if (sorts && sorts.length > 0) {
          for (const sort of sorts) {
            if (sort.field && sort.order) {
              orderBy.push(sort.order === 'desc' ? `-${sort.field}` : sort.field);
            }
          }
        }

        const resp = await devicePackageStore.listDevicePackages(
          deviceId,
          { page: page.currentPage, pageSize: page.pageSize },
          {
            query: formValues?.query,
            needsUpdate: formValues?.needsUpdate,
            isSecurityUpdate: formValues?.isSecurityUpdate,
            packageManager: formValues?.packageManager,
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
      title: $t('ipam.page.packages.name'),
      field: 'name',
      minWidth: 200,
      sortable: true,
    },
    {
      title: $t('ipam.page.packages.currentVersion'),
      field: 'currentVersion',
      width: 160,
      sortable: true,
    },
    {
      title: $t('ipam.page.packages.availableVersion'),
      field: 'availableVersion',
      width: 160,
      sortable: true,
      slots: { default: 'availableVersion' },
    },
    {
      title: $t('ipam.page.packages.needsUpdate'),
      field: 'needsUpdate',
      width: 120,
      sortable: true,
      slots: { default: 'needsUpdate' },
    },
    {
      title: $t('ipam.page.packages.securityUpdate'),
      field: 'isSecurityUpdate',
      width: 120,
      sortable: true,
      slots: { default: 'isSecurityUpdate' },
    },
    {
      title: $t('ipam.page.packages.packageManager'),
      field: 'packageManager',
      width: 130,
      sortable: true,
      slots: { default: 'packageManager' },
    },
    {
      title: $t('ipam.page.packages.description'),
      field: 'description',
      minWidth: 200,
    },
    {
      title: $t('ui.table.updatedAt'),
      field: 'updatedAt',
      formatter: 'formatDateTime',
      width: 160,
      sortable: true,
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

async function handleDeleteAll() {
  if (!selectedDeviceId.value) return;
  try {
    await devicePackageStore.deleteDevicePackages(selectedDeviceId.value);
    notification.success({ message: $t('ipam.page.packages.deleteAllSuccess') });
    stats.value = {};
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}

function formatLastSync(lastSync: string | undefined) {
  if (!lastSync) return '-';
  return new Date(lastSync).toLocaleString();
}
</script>

<template>
  <Page auto-content-height>
    <div v-if="selectedDeviceId && (stats.total ?? 0) > 0" class="mb-4">
      <Row :gutter="16">
        <Col :span="6">
          <Card size="small">
            <Statistic
              :title="$t('ipam.page.packages.totalPackages')"
              :value="stats.total ?? 0"
            />
          </Card>
        </Col>
        <Col :span="6">
          <Card size="small">
            <Statistic
              :title="$t('ipam.page.packages.updatesAvailable')"
              :value="stats.updatesAvailable ?? 0"
              :value-style="{ color: (stats.updatesAvailable ?? 0) > 0 ? '#faad14' : undefined }"
            />
          </Card>
        </Col>
        <Col :span="6">
          <Card size="small">
            <Statistic
              :title="$t('ipam.page.packages.securityUpdates')"
              :value="stats.securityUpdates ?? 0"
              :value-style="{ color: (stats.securityUpdates ?? 0) > 0 ? '#ff4d4f' : undefined }"
            />
          </Card>
        </Col>
        <Col :span="6">
          <Card size="small">
            <Statistic
              :title="$t('ipam.page.packages.lastSync')"
              :value="formatLastSync(stats.lastSync)"
            />
          </Card>
        </Col>
      </Row>
    </div>

    <Grid :table-title="$t('ipam.page.packages.title')">
      <template #toolbar-tools>
        <a-popconfirm
          v-if="selectedDeviceId"
          :cancel-text="$t('ui.button.cancel')"
          :ok-text="$t('ui.button.ok')"
          :title="$t('ipam.page.packages.confirmDeleteAll')"
          @confirm="handleDeleteAll"
        >
          <Button class="mr-2" danger :icon="h(LucideTrash)">
            {{ $t('ipam.page.packages.deleteAll') }}
          </Button>
        </a-popconfirm>
      </template>
      <template #availableVersion="{ row }">
        <span v-if="row.availableVersion" class="text-orange-500 font-medium">
          {{ row.availableVersion }}
        </span>
        <span v-else class="text-gray-400">-</span>
      </template>
      <template #needsUpdate="{ row }">
        <Tag v-if="row.needsUpdate" color="warning">
          {{ $t('ipam.page.packages.needsUpdate') }}
        </Tag>
        <Tag v-else color="success">
          {{ $t('ipam.page.packages.upToDate') }}
        </Tag>
      </template>
      <template #isSecurityUpdate="{ row }">
        <Tag v-if="row.isSecurityUpdate" color="error">
          {{ $t('ipam.page.packages.securityUpdate') }}
        </Tag>
        <span v-else class="text-gray-400">-</span>
      </template>
      <template #packageManager="{ row }">
        <Tag>{{ row.packageManager }}</Tag>
      </template>
    </Grid>
  </Page>
</template>
