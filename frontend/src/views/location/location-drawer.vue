<script lang="ts" setup>
import { ref, computed } from 'vue';

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
  Divider,
  TreeSelect,
} from 'ant-design-vue';

import {
  type ipamservicev1_Location,
  type ipamservicev1_LocationTreeNode,
} from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamLocationStore } from '../../stores/ipam-location.state';
import { formatDateTime as formatDateTimeShared } from '../../datetime';

const locationStore = useIpamLocationStore();
const userStore = useUserStore();

const data = ref<{
  mode: 'create' | 'edit' | 'view';
  row?: ipamservicev1_Location;
  parentId?: string;
}>();
const loading = ref(false);
const locationTreeData = ref<any[]>([]);

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

const formState = ref<{
  name: string;
  description: string;
  locationType: string;
  address: string;
  parentId?: string;
  rackSizeU?: number;
}>({
  name: '',
  description: '',
  locationType: 'LOCATION_TYPE_DATACENTER',
  address: '',
  parentId: undefined,
  rackSizeU: undefined,
});

const isRackType = computed(() => formState.value.locationType === 'LOCATION_TYPE_RACK');

const title = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.location.create');
    case 'edit':
      return $t('ipam.page.location.edit');
    default:
      return $t('ipam.page.location.view');
  }
});

const isCreateMode = computed(() => data.value?.mode === 'create');
const isEditMode = computed(() => data.value?.mode === 'edit');
const isViewMode = computed(() => data.value?.mode === 'view');

function locationTypeToName(type: string | undefined) {
  const option = locationTypeOptions.value.find((o) => o.value === type);
  return option?.label ?? type ?? '';
}

function formatDateTime(value: string | undefined) {
  if (!value) return '-';
  try {
    return formatDateTimeShared(value);
  } catch {
    return value;
  }
}

interface TreeNode {
  key: string;
  value: string;
  title: string;
  children?: TreeNode[];
}

function buildTreeSelectData(nodes: ipamservicev1_LocationTreeNode[]): TreeNode[] {
  return nodes.map((node) => ({
    key: node.location?.id ?? '',
    value: node.location?.id ?? '',
    title: node.location?.name ?? '',
    children: node.children ? buildTreeSelectData(node.children) : undefined,
  }));
}

async function loadLocationTree() {
  try {
    const resp = await locationStore.getLocationTree(undefined, 10);
    locationTreeData.value = buildTreeSelectData((resp.nodes ?? []) as ipamservicev1_LocationTreeNode[]);
  } catch (e) {
    console.error('Failed to load location tree:', e);
  }
}

async function handleSubmit() {
  loading.value = true;
  try {
    const rackSizeU = isRackType.value ? (formState.value.rackSizeU ?? 42) : undefined;
    if (isCreateMode.value) {
      await locationStore.createLocation(
        userStore.tenantId as number,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          locationType: formState.value.locationType as any,
          address: formState.value.address || undefined,
          parentId: formState.value.parentId,
          rackSizeU,
        },
      );
      notification.success({
        message: $t('ipam.page.location.createSuccess'),
      });
    } else if (isEditMode.value && data.value?.row?.id) {
      const updateMask = ['name', 'description', 'locationType', 'address', 'parentId'];
      if (isRackType.value) {
        updateMask.push('rackSizeU');
      }
      await locationStore.updateLocation(
        data.value.row.id,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          locationType: formState.value.locationType as any,
          address: formState.value.address || undefined,
          parentId: formState.value.parentId,
          rackSizeU,
        },
        updateMask,
      );
      notification.success({
        message: $t('ipam.page.location.updateSuccess'),
      });
    }
    drawerApi.close();
  } catch (e) {
    console.error('Failed to save location:', e);
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
    locationType: 'LOCATION_TYPE_DATACENTER',
    address: '',
    parentId: undefined,
    rackSizeU: undefined,
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
        row?: ipamservicev1_Location;
        parentId?: string;
      };

      await loadLocationTree();

      if (data.value?.mode === 'create') {
        resetForm();
        if (data.value.parentId) {
          formState.value.parentId = data.value.parentId;
        }
      } else if (data.value?.row) {
        formState.value = {
          name: data.value.row.name ?? '',
          description: data.value.row.description ?? '',
          locationType: data.value.row.locationType ?? 'LOCATION_TYPE_DATACENTER',
          address: data.value.row.address ?? '',
          parentId: data.value.row.parentId,
          rackSizeU: data.value.row.rackSizeU ?? undefined,
        };
      }
    }
  },
});

const location = computed(() => data.value?.row);
</script>

<template>
  <Drawer :title="title" :footer="false">
    <!-- View Mode -->
    <template v-if="location && isViewMode">
      <Descriptions :column="1" bordered size="small">
        <DescriptionsItem :label="$t('ipam.page.location.name')">
          {{ location.name }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.location.locationType')">
          {{ locationTypeToName(location.locationType) }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.location.address')">
          {{ location.address || '-' }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.location.description')">
          {{ location.description || '-' }}
        </DescriptionsItem>
        <DescriptionsItem
          v-if="location.locationType === 'LOCATION_TYPE_RACK'"
          :label="$t('ipam.page.location.rackSizeU')"
        >
          {{ location.rackSizeU ? `${location.rackSizeU}U` : '-' }}
        </DescriptionsItem>
      </Descriptions>

      <Divider>{{ $t('ui.table.timestamps') }}</Divider>
      <Descriptions :column="2" bordered size="small">
        <DescriptionsItem :label="$t('ui.table.createdAt')">
          {{ formatDateTime(location.createdAt) }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ui.table.updatedAt')">
          {{ formatDateTime(location.updatedAt) }}
        </DescriptionsItem>
      </Descriptions>
    </template>

    <!-- Create/Edit Mode -->
    <template v-else-if="isCreateMode || isEditMode">
      <Form layout="vertical" :model="formState" @finish="handleSubmit">
        <FormItem
          :label="$t('ipam.page.location.name')"
          name="name"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Input
            v-model:value="formState.name"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.location.locationType')" name="locationType">
          <Select
            v-model:value="formState.locationType"
            :options="locationTypeOptions"
          />
        </FormItem>

        <FormItem
          v-if="isRackType"
          :label="$t('ipam.page.location.rackSizeU')"
          name="rackSizeU"
        >
          <InputNumber
            v-model:value="formState.rackSizeU"
            :min="1"
            :max="50"
            :placeholder="'42'"
            style="width: 100%"
          />
          <div class="text-gray-500 text-xs mt-1">
            {{ $t('ipam.page.location.rackSizeUHelp') }}
          </div>
        </FormItem>

        <FormItem :label="$t('ipam.page.location.parent')" name="parentId">
          <TreeSelect
            v-model:value="formState.parentId"
            :tree-data="locationTreeData"
            :placeholder="$t('ui.placeholder.select')"
            allow-clear
            tree-default-expand-all
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.location.address')" name="address">
          <Input
            v-model:value="formState.address"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="500"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.location.description')" name="description">
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
