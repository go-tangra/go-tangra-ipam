<script lang="ts" setup>
import type { VxeGridProps } from 'shell/adapter/vxe-table';

import { h, ref, computed, onMounted } from 'vue';

import { Page, useVbenDrawer, type VbenFormProps } from 'shell/vben/common-ui';
import {
  LucideEye,
  LucideTrash,
  LucidePencil,
  LucidePlus,
  LucideServer,
} from 'shell/vben/icons';

import { notification, Space, Button, Tree, Card, Empty, Drawer, Popconfirm } from 'ant-design-vue';
import type { TreeProps } from 'ant-design-vue';

import { useVbenVxeGrid } from 'shell/adapter/vxe-table';
import {
  type ipamservicev1_Location,
  type ipamservicev1_LocationTreeNode,
  type ipamservicev1_Device,
} from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamLocationStore } from '../../stores/ipam-location.state';

import LocationDrawer from './location-drawer.vue';
import RackVisualization from './rack-visualization.vue';

const locationStore = useIpamLocationStore();

const treeData = ref<TreeProps['treeData']>([]);
const selectedLocationId = ref<string | undefined>(undefined);
const selectedLocation = ref<ipamservicev1_Location | null>(null);
const treeLoading = ref(false);
const expandedKeys = ref<(string | number)[]>([]);

const locationTypeOptions = computed(() => [
  { value: 'LOCATION_TYPE_DATACENTER', label: $t('ipam.enum.locationType.dataCenter') },
  { value: 'LOCATION_TYPE_BUILDING', label: $t('ipam.enum.locationType.building') },
  { value: 'LOCATION_TYPE_FLOOR', label: $t('ipam.enum.locationType.floor') },
  { value: 'LOCATION_TYPE_ROOM', label: $t('ipam.enum.locationType.room') },
  { value: 'LOCATION_TYPE_RACK', label: $t('ipam.enum.locationType.rack') },
  { value: 'LOCATION_TYPE_SITE', label: $t('ipam.enum.locationType.site') },
  { value: 'LOCATION_TYPE_BRANCH', label: $t('ipam.enum.locationType.branch') },
  { value: 'LOCATION_TYPE_REGION', label: $t('ipam.enum.locationType.region') },
  { value: 'LOCATION_TYPE_COUNTRY', label: $t('ipam.enum.locationType.country') },
  { value: 'LOCATION_TYPE_CITY', label: $t('ipam.enum.locationType.city') },
]);

function locationTypeToName(type: string | undefined) {
  const option = locationTypeOptions.value.find((o) => o.value === type);
  return option?.label ?? type ?? '';
}

interface TreeNode {
  key: string;
  title: string;
  children?: TreeNode[];
  location: ipamservicev1_Location | undefined;
}

function formatLocationTitle(location: ipamservicev1_Location | undefined): string {
  if (!location) return '';
  const typeName = locationTypeToName(location.locationType);
  const name = location.name ?? '';
  return typeName ? `${typeName} ${name}` : name;
}

function buildTreeData(nodes: ipamservicev1_LocationTreeNode[]): TreeNode[] {
  return nodes.map((node) => ({
    key: node.location?.id ?? '',
    title: formatLocationTitle(node.location),
    children: node.children ? buildTreeData(node.children) : undefined,
    location: node.location,
  }));
}

async function loadLocationTree() {
  treeLoading.value = true;
  try {
    const resp = await locationStore.getLocationTree(undefined, 10);
    treeData.value = buildTreeData((resp.nodes ?? []) as ipamservicev1_LocationTreeNode[]);
    if (treeData.value.length > 0) {
      expandedKeys.value = treeData.value.map((n) => n.key);
    }
  } catch (e) {
    console.error('Failed to load location tree:', e);
  } finally {
    treeLoading.value = false;
  }
}

function handleTreeSelect(
  keys: (string | number)[],
  info: { node: any },
) {
  if (keys.length > 0) {
    selectedLocationId.value = String(keys[0]);
    selectedLocation.value = info.node.location ?? null;
    gridApi.reload();
  } else {
    selectedLocationId.value = undefined;
    selectedLocation.value = null;
  }
}

const formOptions: VbenFormProps = {
  collapsed: false,
  showCollapseButton: false,
  submitOnEnter: true,
  schema: [
    {
      component: 'Input',
      fieldName: 'nameFilter',
      label: $t('ipam.page.location.name'),
      componentProps: {
        placeholder: $t('ui.placeholder.input'),
        allowClear: true,
      },
    },
  ],
};

const gridOptions: VxeGridProps<ipamservicev1_Location> = {
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
        const resp = await locationStore.listLocations(
          { page: page.currentPage, pageSize: page.pageSize },
          {
            parentId: selectedLocationId.value,
            query: formValues?.nameFilter,
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
      title: $t('ipam.page.location.name'),
      field: 'name',
      minWidth: 150,
      slots: { default: 'name' },
    },
    {
      title: $t('ipam.page.location.locationType'),
      field: 'locationType',
      width: 130,
      formatter: ({ cellValue }) => locationTypeToName(cellValue),
    },
    { title: $t('ipam.page.location.address'), field: 'address', minWidth: 200 },
    { title: $t('ipam.page.location.description'), field: 'description', minWidth: 150 },
    {
      title: $t('ui.table.createdAt'),
      field: 'createTime',
      formatter: 'formatDateTime',
      width: 160,
    },
    {
      title: $t('ui.table.action'),
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      width: 180,
    },
  ],
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions, formOptions });

const [LocationDrawerComponent, locationDrawerApi] = useVbenDrawer({
  connectedComponent: LocationDrawer,
  onOpenChange(isOpen: boolean) {
    if (!isOpen) {
      loadLocationTree();
      gridApi.query();
    }
  },
});

function openDrawer(
  row: ipamservicev1_Location,
  mode: 'create' | 'edit' | 'view',
  parentId?: string,
) {
  locationDrawerApi.setData({ row, mode, parentId });
  locationDrawerApi.open();
}

function handleView(row: ipamservicev1_Location) {
  openDrawer(row, 'view');
}

function handleEdit(row: ipamservicev1_Location) {
  openDrawer(row, 'edit');
}

function handleCreate() {
  openDrawer({} as ipamservicev1_Location, 'create', selectedLocationId.value);
}

function handleCreateChild(row: ipamservicev1_Location) {
  openDrawer({} as ipamservicev1_Location, 'create', row.id);
}

async function handleDelete(row: ipamservicev1_Location) {
  if (!row.id) return;
  try {
    await locationStore.deleteLocation(row.id);
    notification.success({ message: $t('ipam.page.location.deleteSuccess') });
    await loadLocationTree();
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}

// Tree node actions
function handleEditFromTree(location: ipamservicev1_Location | undefined) {
  if (!location) return;
  openDrawer(location, 'edit');
}

async function handleDeleteFromTree(location: ipamservicev1_Location | undefined) {
  if (!location?.id) return;
  try {
    await locationStore.deleteLocation(location.id);
    notification.success({ message: $t('ipam.page.location.deleteSuccess') });
    // Clear selection if deleted node was selected
    if (selectedLocationId.value === location.id) {
      selectedLocationId.value = undefined;
      selectedLocation.value = null;
    }
    await loadLocationTree();
    await gridApi.query();
  } catch {
    notification.error({ message: $t('ui.notification.delete_failed') });
  }
}

// Rack visualization drawer
const rackDrawerVisible = ref(false);
const rackDrawerLocation = ref<ipamservicev1_Location | null>(null);
const rackVisualizationRef = ref<InstanceType<typeof RackVisualization> | null>(null);

function isRackType(location: ipamservicev1_Location | null | undefined): boolean {
  return location?.locationType === 'LOCATION_TYPE_RACK';
}

function handleViewRack(row: ipamservicev1_Location) {
  rackDrawerLocation.value = row;
  rackDrawerVisible.value = true;
}

function handleRackDrawerClose() {
  rackDrawerVisible.value = false;
  rackDrawerLocation.value = null;
}

function handleRackSlotClick(position: number) {
  // Could open device creation drawer with pre-filled rack position
  console.log('Slot clicked:', position);
}

function handleRackDeviceClick(device: ipamservicev1_Device) {
  // Could open device view/edit drawer
  console.log('Device clicked:', device);
}

onMounted(() => {
  loadLocationTree();
});
</script>

<template>
  <Page auto-content-height>
    <div class="flex h-full gap-4">
      <!-- Left: Location Tree -->
      <Card
        class="w-[346px] shrink-0"
        :title="$t('ipam.page.location.tree')"
        :loading="treeLoading"
      >
        <template #extra>
          <Button type="link" size="small" @click="handleCreate">
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
            <template #title="{ title, location }">
              <div class="tree-node-title group flex items-center justify-between pr-1">
                <span class="truncate flex-1">{{ title }}</span>
                <span class="tree-actions flex items-center opacity-0 group-hover:opacity-100 transition-opacity">
                  <Button
                    type="text"
                    size="small"
                    class="!px-1"
                    :title="$t('ui.button.edit')"
                    @click.stop="handleEditFromTree(location)"
                  >
                    <template #icon>
                      <LucidePencil class="size-3" />
                    </template>
                  </Button>
                  <Popconfirm
                    :title="$t('ipam.page.location.confirmDelete')"
                    :ok-text="$t('ui.button.ok')"
                    :cancel-text="$t('ui.button.cancel')"
                    @confirm.stop="handleDeleteFromTree(location)"
                  >
                    <Button
                      type="text"
                      size="small"
                      danger
                      class="!px-1"
                      :title="$t('ui.button.delete', { moduleName: '' })"
                      @click.stop
                    >
                      <template #icon>
                        <LucideTrash class="size-3" />
                      </template>
                    </Button>
                  </Popconfirm>
                </span>
              </div>
            </template>
          </Tree>
        </div>
        <Empty v-else :description="$t('ui.table.empty')" />
      </Card>

      <!-- Right: Location List -->
      <div class="flex-1 min-w-0">
        <Grid :table-title="selectedLocation ? selectedLocation.name : $t('ipam.page.location.title')">
          <template #toolbar-tools>
            <Button class="mr-2" type="primary" @click="handleCreate">
              {{ $t('ipam.page.location.create') }}
            </Button>
          </template>
          <template #name="{ row }">
            <span>{{ row.name }}</span>
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
                v-if="isRackType(row)"
                type="link"
                size="small"
                :icon="h(LucideServer)"
                :title="$t('ipam.page.rack.title')"
                @click.stop="handleViewRack(row)"
              />
              <Button
                type="link"
                size="small"
                :icon="h(LucidePlus)"
                :title="$t('ipam.page.location.createChild')"
                @click.stop="handleCreateChild(row)"
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
                :title="$t('ipam.page.location.confirmDelete')"
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
      </div>
    </div>

    <LocationDrawerComponent />

    <!-- Rack Visualization Drawer -->
    <Drawer
      v-model:open="rackDrawerVisible"
      :title="rackDrawerLocation?.name ? `${$t('ipam.page.rack.title')} - ${rackDrawerLocation.name}` : $t('ipam.page.rack.title')"
      width="500"
      :destroy-on-close="true"
      @close="handleRackDrawerClose"
    >
      <RackVisualization
        v-if="rackDrawerLocation?.id"
        ref="rackVisualizationRef"
        :location-id="rackDrawerLocation.id"
        :rack-size-u="rackDrawerLocation.rackSizeU ?? 42"
        @slot-click="handleRackSlotClick"
        @device-click="handleRackDeviceClick"
      />
    </Drawer>
  </Page>
</template>
