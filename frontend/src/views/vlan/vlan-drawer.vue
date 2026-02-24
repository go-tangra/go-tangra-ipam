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
  Tag,
} from 'ant-design-vue';

import { type ipamservicev1_Vlan } from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamVlanStore } from '../../stores/ipam-vlan.state';
import { useIpamLocationStore } from '../../stores/ipam-location.state';

const vlanStore = useIpamVlanStore();
const locationStore = useIpamLocationStore();
const userStore = useUserStore();

const data = ref<{
  mode: 'create' | 'edit' | 'view';
  row?: ipamservicev1_Vlan;
}>();
const loading = ref(false);
const locations = ref<Array<{ value: string; label: string }>>([]);

const statusOptions = computed(() => [
  { value: 'VLAN_STATUS_ACTIVE', label: $t('ipam.enum.vlanStatus.active') },
  { value: 'VLAN_STATUS_INACTIVE', label: $t('ipam.enum.vlanStatus.inactive') },
  { value: 'VLAN_STATUS_DEPRECATED', label: $t('ipam.enum.vlanStatus.deprecated') },
]);

const formState = ref<{
  vlanId: number | undefined;
  name: string;
  description: string;
  locationId?: string;
  status: string;
}>({
  vlanId: undefined,
  name: '',
  description: '',
  locationId: undefined,
  status: 'VLAN_STATUS_ACTIVE',
});

const title = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.vlan.create');
    case 'edit':
      return $t('ipam.page.vlan.edit');
    default:
      return $t('ipam.page.vlan.view');
  }
});

const isCreateMode = computed(() => data.value?.mode === 'create');
const isEditMode = computed(() => data.value?.mode === 'edit');
const isViewMode = computed(() => data.value?.mode === 'view');

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

async function handleSubmit() {
  loading.value = true;
  try {
    if (isCreateMode.value) {
      await vlanStore.createVlan(
        userStore.tenantId as number,
        {
          vlanId: formState.value.vlanId,
          name: formState.value.name,
          description: formState.value.description || undefined,
          locationId: formState.value.locationId,
          status: formState.value.status as any,
        },
      );
      notification.success({
        message: $t('ipam.page.vlan.createSuccess'),
      });
    } else if (isEditMode.value && data.value?.row?.id) {
      await vlanStore.updateVlan(
        data.value.row.id,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          locationId: formState.value.locationId,
          status: formState.value.status as any,
        },
        ['name', 'description', 'locationId', 'status'],
      );
      notification.success({
        message: $t('ipam.page.vlan.updateSuccess'),
      });
    }
    drawerApi.close();
  } catch (e) {
    console.error('Failed to save VLAN:', e);
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
    vlanId: undefined,
    name: '',
    description: '',
    locationId: undefined,
    status: 'VLAN_STATUS_ACTIVE',
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
        row?: ipamservicev1_Vlan;
      };

      await loadLocations();

      if (data.value?.mode === 'create') {
        resetForm();
      } else if (data.value?.row) {
        formState.value = {
          vlanId: data.value.row.vlanId,
          name: data.value.row.name ?? '',
          description: data.value.row.description ?? '',
          locationId: data.value.row.locationId,
          status: data.value.row.status ?? 'VLAN_STATUS_ACTIVE',
        };
      }
    }
  },
});

const vlan = computed(() => data.value?.row);
</script>

<template>
  <Drawer :title="title" :footer="false">
    <!-- View Mode -->
    <template v-if="vlan && isViewMode">
      <Descriptions :column="1" bordered size="small">
        <DescriptionsItem :label="$t('ipam.page.vlan.vlanId')">
          {{ vlan.vlanId }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.vlan.name')">
          {{ vlan.name || '-' }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.vlan.status')">
          <Tag :color="statusToColor(vlan.status)">
            {{ statusToName(vlan.status) }}
          </Tag>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.vlan.description')">
          {{ vlan.description || '-' }}
        </DescriptionsItem>
      </Descriptions>
    </template>

    <!-- Create/Edit Mode -->
    <template v-else-if="isCreateMode || isEditMode">
      <Form layout="vertical" :model="formState" @finish="handleSubmit">
        <FormItem
          :label="$t('ipam.page.vlan.vlanId')"
          name="vlanId"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <InputNumber
            v-model:value="formState.vlanId"
            :min="1"
            :max="4094"
            :disabled="isEditMode"
            style="width: 100%"
          />
        </FormItem>

        <FormItem
          :label="$t('ipam.page.vlan.name')"
          name="name"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Input
            v-model:value="formState.name"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.vlan.status')" name="status">
          <Select
            v-model:value="formState.status"
            :options="statusOptions"
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

        <FormItem :label="$t('ipam.page.vlan.description')" name="description">
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
