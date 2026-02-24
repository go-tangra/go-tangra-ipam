<script lang="ts" setup>
import { ref, computed } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { useUserStore } from 'shell/vben/stores';

import {
  Form,
  FormItem,
  Input,
  Button,
  notification,
  Textarea,
  Select,
  Descriptions,
  DescriptionsItem,
  Tag,
  Spin,
  Alert,
} from 'ant-design-vue';
import { LucideRefreshCw, LucideCheckCircle } from 'shell/vben/icons';

import { type ipamservicev1_IpAddress } from '../../api/proto-types';
import { type SuggestedAddress } from '../../api/services';
import { $t } from 'shell/locales';
import { useIpamIpAddressStore } from '../../stores/ipam-ip-address.state';
import { useIpamSubnetStore } from '../../stores/ipam-subnet.state';
import { useIpamDeviceStore } from '../../stores/ipam-device.state';

const ipAddressStore = useIpamIpAddressStore();
const subnetStore = useIpamSubnetStore();
const deviceStore = useIpamDeviceStore();
const userStore = useUserStore();

const data = ref<{
  mode: 'create' | 'edit' | 'view';
  row?: ipamservicev1_IpAddress;
}>();
const loading = ref(false);
const subnets = ref<Array<{ value: string; label: string }>>([]);
const devices = ref<Array<{ value: string; label: string }>>([]);

// Suggestion state
const suggestedAddresses = ref<SuggestedAddress[]>([]);
const suggestLoading = ref(false);
const suggestError = ref('');
const totalUnallocated = ref<number | undefined>();
const skippedAddresses = ref<string[]>([]);
const selectedSuggestion = ref<string | undefined>();

const statusOptions = computed(() => [
  { value: 'IP_ADDRESS_STATUS_ACTIVE', label: $t('ipam.enum.addressStatus.active') },
  { value: 'IP_ADDRESS_STATUS_RESERVED', label: $t('ipam.enum.addressStatus.reserved') },
  { value: 'IP_ADDRESS_STATUS_DHCP', label: $t('ipam.enum.addressStatus.dhcp') },
  { value: 'IP_ADDRESS_STATUS_DEPRECATED', label: $t('ipam.enum.addressStatus.deprecated') },
  { value: 'IP_ADDRESS_STATUS_OFFLINE', label: $t('ipam.enum.addressStatus.offline') },
]);

const formState = ref<{
  address: string;
  hostname: string;
  description: string;
  subnetId?: string;
  deviceId?: string;
  status: string;
  macAddress: string;
}>({
  address: '',
  hostname: '',
  description: '',
  subnetId: undefined,
  deviceId: undefined,
  status: 'IP_ADDRESS_STATUS_ACTIVE',
  macAddress: '',
});

const title = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.ipAddress.create');
    case 'edit':
      return $t('ipam.page.ipAddress.edit');
    default:
      return $t('ipam.page.ipAddress.view');
  }
});

const isCreateMode = computed(() => data.value?.mode === 'create');
const isEditMode = computed(() => data.value?.mode === 'edit');
const isViewMode = computed(() => data.value?.mode === 'view');

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

async function loadSubnets() {
  try {
    const resp = await subnetStore.listSubnets({ page: 1, pageSize: 100 });
    subnets.value = (resp.items ?? []).map((s) => ({
      value: s.id ?? '',
      label: `${s.cidr} - ${s.name || 'Unnamed'}`,
    }));
  } catch (e) {
    console.error('Failed to load subnets:', e);
  }
}

async function loadDevices() {
  try {
    const resp = await deviceStore.listDevices({ page: 1, pageSize: 100 });
    devices.value = (resp.items ?? []).map((d) => ({
      value: d.id ?? '',
      label: d.name ?? '',
    }));
  } catch (e) {
    console.error('Failed to load devices:', e);
  }
}

async function fetchSuggestions(subnetId: string) {
  suggestLoading.value = true;
  suggestError.value = '';
  suggestedAddresses.value = [];
  selectedSuggestion.value = undefined;
  try {
    const resp = await ipAddressStore.suggestAvailableAddresses(
      subnetId,
      5,
      skippedAddresses.value,
    );
    suggestedAddresses.value = resp.addresses ?? [];
    totalUnallocated.value = resp.totalUnallocated;
  } catch (e) {
    console.error('Failed to fetch suggestions:', e);
    suggestError.value = $t('ipam.page.ipAddress.suggestFailed');
  } finally {
    suggestLoading.value = false;
  }
}

function onSubnetChange(subnetId: string) {
  suggestedAddresses.value = [];
  skippedAddresses.value = [];
  totalUnallocated.value = undefined;
  selectedSuggestion.value = undefined;
  if (subnetId) {
    fetchSuggestions(subnetId);
  }
}

function selectSuggestedAddress(addr: SuggestedAddress) {
  formState.value.address = addr.address;
  selectedSuggestion.value = addr.address;
}

function refreshSuggestions() {
  if (!formState.value.subnetId) return;
  // Add current suggestions to skip list to get different results
  for (const addr of suggestedAddresses.value) {
    if (!skippedAddresses.value.includes(addr.address)) {
      skippedAddresses.value.push(addr.address);
    }
  }
  fetchSuggestions(formState.value.subnetId);
}

async function handleSubmit() {
  loading.value = true;
  try {
    if (isCreateMode.value) {
      await ipAddressStore.createIpAddress(
        userStore.tenantId as number,
        {
          address: formState.value.address,
          hostname: formState.value.hostname || undefined,
          description: formState.value.description || undefined,
          subnetId: formState.value.subnetId,
          deviceId: formState.value.deviceId,
          status: formState.value.status as any,
          macAddress: formState.value.macAddress || undefined,
        },
      );
      notification.success({
        message: $t('ipam.page.ipAddress.createSuccess'),
      });
    } else if (isEditMode.value && data.value?.row?.id) {
      await ipAddressStore.updateIpAddress(
        data.value.row.id,
        {
          hostname: formState.value.hostname || undefined,
          description: formState.value.description || undefined,
          deviceId: formState.value.deviceId,
          status: formState.value.status as any,
          macAddress: formState.value.macAddress || undefined,
        },
        ['hostname', 'description', 'deviceId', 'status', 'macAddress'],
      );
      notification.success({
        message: $t('ipam.page.ipAddress.updateSuccess'),
      });
    }
    drawerApi.close();
  } catch (e) {
    console.error('Failed to save IP address:', e);
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
    address: '',
    hostname: '',
    description: '',
    subnetId: undefined,
    deviceId: undefined,
    status: 'IP_ADDRESS_STATUS_ACTIVE',
    macAddress: '',
  };
  suggestedAddresses.value = [];
  suggestLoading.value = false;
  suggestError.value = '';
  totalUnallocated.value = undefined;
  skippedAddresses.value = [];
  selectedSuggestion.value = undefined;
}

const [Drawer, drawerApi] = useVbenDrawer({
  onCancel() {
    drawerApi.close();
  },

  async onOpenChange(isOpen) {
    if (isOpen) {
      data.value = drawerApi.getData() as {
        mode: 'create' | 'edit' | 'view';
        row?: ipamservicev1_IpAddress;
      };

      await Promise.all([loadSubnets(), loadDevices()]);

      if (data.value?.mode === 'create') {
        resetForm();
      } else if (data.value?.row) {
        formState.value = {
          address: data.value.row.address ?? '',
          hostname: data.value.row.hostname ?? '',
          description: data.value.row.description ?? '',
          subnetId: data.value.row.subnetId,
          deviceId: data.value.row.deviceId,
          status: data.value.row.status ?? 'IP_ADDRESS_STATUS_ACTIVE',
          macAddress: data.value.row.macAddress ?? '',
        };
      }
    }
  },
});

const ipAddress = computed(() => data.value?.row);
</script>

<template>
  <Drawer :title="title" :footer="false">
    <!-- View Mode -->
    <template v-if="ipAddress && isViewMode">
      <Descriptions :column="1" bordered size="small">
        <DescriptionsItem :label="$t('ipam.page.ipAddress.address')">
          <span class="font-mono">{{ ipAddress.address }}</span>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.ipAddress.hostname')">
          {{ ipAddress.hostname || '-' }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.ipAddress.status')">
          <Tag :color="statusToColor(ipAddress.status)">
            {{ statusToName(ipAddress.status) }}
          </Tag>
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.ipAddress.macAddress')">
          {{ ipAddress.macAddress || '-' }}
        </DescriptionsItem>
        <DescriptionsItem :label="$t('ipam.page.ipAddress.description')">
          {{ ipAddress.description || '-' }}
        </DescriptionsItem>
      </Descriptions>
    </template>

    <!-- Create/Edit Mode -->
    <template v-else-if="isCreateMode || isEditMode">
      <Form layout="vertical" :model="formState" @finish="handleSubmit">
        <FormItem
          v-if="isCreateMode"
          :label="$t('ipam.page.subnet.title')"
          name="subnetId"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Select
            v-model:value="formState.subnetId"
            :options="subnets"
            :placeholder="$t('ui.placeholder.select')"
            show-search
            :filter-option="(input: string, option: any) =>
              option.label.toLowerCase().includes(input.toLowerCase())"
            @change="onSubnetChange"
          />
        </FormItem>

        <!-- Suggested Addresses (create mode only, after subnet selected) -->
        <div
          v-if="isCreateMode && formState.subnetId"
          class="mb-4"
        >
          <div class="mb-2 flex items-center justify-between">
            <span class="text-sm font-medium">
              {{ $t('ipam.page.ipAddress.suggestedAddresses') }}
            </span>
            <Button
              size="small"
              :loading="suggestLoading"
              @click="refreshSuggestions"
            >
              <template #icon><LucideRefreshCw class="size-4" /></template>
              {{ $t('ipam.page.ipAddress.refreshSuggestions') }}
            </Button>
          </div>

          <!-- Loading -->
          <div v-if="suggestLoading" class="flex justify-center py-4">
            <Spin />
          </div>

          <!-- Error -->
          <Alert
            v-else-if="suggestError"
            type="warning"
            :message="suggestError"
            show-icon
            class="mb-2"
          />

          <!-- No suggestions -->
          <Alert
            v-else-if="suggestedAddresses.length === 0 && !suggestLoading"
            type="info"
            :message="$t('ipam.page.ipAddress.noSuggestions')"
            show-icon
            class="mb-2"
          />

          <!-- Suggestion cards -->
          <div v-else class="space-y-2">
            <div
              v-for="addr in suggestedAddresses"
              :key="addr.address"
              class="suggestion-card cursor-pointer rounded-md border px-3 py-2 transition-colors"
              :class="{
                'suggestion-card-selected': selectedSuggestion === addr.address,
              }"
              @click="selectSuggestedAddress(addr)"
            >
              <div class="flex items-center justify-between">
                <span class="font-mono text-sm font-medium">{{ addr.address }}</span>
                <LucideCheckCircle
                  v-if="selectedSuggestion === addr.address"
                  class="size-4" style="color: #3b82f6"
                />
              </div>
              <div class="mt-1 flex gap-1">
                <Tag v-if="addr.pingFree" color="success" class="!text-xs">
                  {{ $t('ipam.page.ipAddress.pingVerified') }}
                </Tag>
                <Tag v-else color="warning" class="!text-xs">
                  ICMP
                </Tag>
                <Tag v-if="addr.portScanFree" color="success" class="!text-xs">
                  {{ $t('ipam.page.ipAddress.portScanVerified') }}
                </Tag>
                <Tag v-else color="warning" class="!text-xs">
                  TCP
                </Tag>
              </div>
            </div>

            <!-- Total available count -->
            <div v-if="totalUnallocated !== undefined" class="mt-1 text-xs text-gray-400 dark:text-gray-500">
              {{ $t('ipam.page.ipAddress.totalAvailable', { count: totalUnallocated }) }}
            </div>
          </div>
        </div>

        <FormItem
          v-if="isCreateMode"
          :label="$t('ipam.page.ipAddress.address')"
          name="address"
          :rules="[{ required: true, message: $t('ui.formRules.required') }]"
        >
          <Input
            v-model:value="formState.address"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="45"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.ipAddress.hostname')" name="hostname">
          <Input
            v-model:value="formState.hostname"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="255"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.ipAddress.status')" name="status">
          <Select
            v-model:value="formState.status"
            :options="statusOptions"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.device.title')" name="deviceId">
          <Select
            v-model:value="formState.deviceId"
            :options="devices"
            :placeholder="$t('ui.placeholder.select')"
            allow-clear
            show-search
            :filter-option="(input: string, option: any) =>
              option.label.toLowerCase().includes(input.toLowerCase())"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.ipAddress.macAddress')" name="macAddress">
          <Input
            v-model:value="formState.macAddress"
            :placeholder="$t('ui.placeholder.input')"
            :maxlength="17"
          />
        </FormItem>

        <FormItem :label="$t('ipam.page.ipAddress.description')" name="description">
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

<style scoped>
.suggestion-card {
  border-color: #e5e7eb;
}
.suggestion-card:hover {
  border-color: #60a5fa;
  background-color: #eff6ff;
}
.suggestion-card-selected {
  border-color: #3b82f6;
  background-color: #eff6ff;
}
:global(.dark) .suggestion-card {
  border-color: #374151;
}
:global(.dark) .suggestion-card:hover {
  border-color: #2563eb;
  background-color: rgb(23 37 84 / 0.5);
}
:global(.dark) .suggestion-card-selected {
  border-color: #2563eb;
  background-color: rgb(23 37 84 / 0.5);
}
</style>
