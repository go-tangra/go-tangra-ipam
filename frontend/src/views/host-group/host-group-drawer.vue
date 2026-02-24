<script lang="ts" setup>
import { ref, computed, h } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { useUserStore } from 'shell/vben/stores';
import { LucideTrash, LucideServer } from 'shell/vben/icons';

import {
  Form,
  FormItem,
  Input,
  Textarea,
  Button,
  notification,
  Select,
  Table,
  Space,
  Tag,
  AutoComplete,
} from 'ant-design-vue';

import {
  type ipamservicev1_HostGroup,
  type ipamservicev1_HostGroupMember,
} from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamHostGroupStore } from '../../stores/ipam-host-group.state';
import { useIpamDeviceStore } from '../../stores/ipam-device.state';

const hostGroupStore = useIpamHostGroupStore();
const deviceStore = useIpamDeviceStore();
const userStore = useUserStore();

interface DrawerData {
  mode: 'create' | 'edit' | 'view';
  row?: ipamservicev1_HostGroup;
}

const data = ref<DrawerData>();
const loading = ref(false);
const membersLoading = ref(false);

// Form state
const formState = ref({
  name: '',
  description: '',
  status: 'HOST_GROUP_STATUS_ACTIVE' as string,
});

// Members state
const members = ref<ipamservicev1_HostGroupMember[]>([]);
const selectedDeviceId = ref<string>('');

// Device search for autocomplete
const deviceSuggestions = ref<{ value: string; label: string; device: any }[]>([]);
const searchLoading = ref(false);
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

async function handleDeviceSearch(searchText: string) {
  if (!searchText || searchText.length < 2) {
    deviceSuggestions.value = [];
    return;
  }

  // Debounce the search
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer);
  }

  searchDebounceTimer = setTimeout(async () => {
    searchLoading.value = true;
    try {
      const resp = await deviceStore.listDevices(
        { page: 1, pageSize: 10 },
        { query: searchText },
      );
      deviceSuggestions.value = (resp.items ?? []).map((device) => ({
        value: device.id ?? '',
        label: device.primaryIp
          ? `${device.name} (${device.primaryIp})`
          : device.name ?? '',
        device,
      }));
    } catch (e) {
      console.error('Failed to fetch device suggestions:', e);
      deviceSuggestions.value = [];
    } finally {
      searchLoading.value = false;
    }
  }, 300);
}

function handleSelectDevice(value: string | number | { value: string | number; label?: string }) {
  const actualValue = typeof value === 'object' && value !== null ? value.value : value;
  selectedDeviceId.value = String(actualValue);
}

const statusOptions = computed(() => [
  { value: 'HOST_GROUP_STATUS_ACTIVE', label: $t('ipam.enum.hostGroupStatus.active') },
  { value: 'HOST_GROUP_STATUS_INACTIVE', label: $t('ipam.enum.hostGroupStatus.inactive') },
]);

function deviceTypeToName(type: number | undefined) {
  switch (type) {
    case 1:
      return $t('ipam.enum.deviceType.server');
    case 2:
      return $t('ipam.enum.deviceType.virtualMachine');
    case 3:
      return $t('ipam.enum.deviceType.router');
    case 4:
      return $t('ipam.enum.deviceType.switch');
    case 5:
      return $t('ipam.enum.deviceType.firewall');
    case 6:
      return $t('ipam.enum.deviceType.loadBalancer');
    case 12:
      return $t('ipam.enum.deviceType.container');
    default:
      return $t('ipam.enum.deviceType.other');
  }
}

function deviceStatusToColor(status: number | undefined) {
  switch (status) {
    case 1:
      return '#52C41A'; // Active
    case 2:
      return '#1890FF'; // Planned
    case 3:
      return '#722ED1'; // Staged
    case 4:
      return '#8C8C8C'; // Decommissioned
    case 5:
      return '#FA8C16'; // Offline
    case 6:
      return '#F5222D'; // Failed
    default:
      return '#C9CDD4';
  }
}

function deviceStatusToName(status: number | undefined) {
  switch (status) {
    case 1:
      return $t('ipam.enum.deviceStatus.active');
    case 2:
      return 'Planned';
    case 3:
      return 'Staged';
    case 4:
      return $t('ipam.enum.deviceStatus.decommissioned');
    case 5:
      return 'Offline';
    case 6:
      return 'Failed';
    default:
      return 'Unknown';
  }
}

const isReadonly = computed(() => data.value?.mode === 'view');
const isEdit = computed(() => data.value?.mode === 'edit');
const drawerTitle = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.hostGroup.create');
    case 'edit':
      return $t('ipam.page.hostGroup.edit');
    case 'view':
      return $t('ipam.page.hostGroup.view');
    default:
      return '';
  }
});

const memberColumns = computed(() => [
  {
    title: $t('ipam.page.hostGroup.deviceName'),
    dataIndex: 'deviceName',
    customRender: ({ record }: { record: ipamservicev1_HostGroupMember }) => {
      return h('div', { class: 'flex items-center gap-2' }, [
        h(LucideServer, { class: 'size-4 text-muted-foreground' }),
        h('span', { class: 'font-medium' }, record.deviceName ?? 'Unknown'),
      ]);
    },
  },
  {
    title: $t('ipam.page.hostGroup.deviceType'),
    dataIndex: 'deviceType',
    width: 140,
    customRender: ({ record }: { record: ipamservicev1_HostGroupMember }) => {
      return h(Tag, { color: 'blue' }, { default: () => deviceTypeToName(record.deviceType) });
    },
  },
  {
    title: $t('ipam.page.hostGroup.deviceStatus'),
    dataIndex: 'deviceStatus',
    width: 120,
    customRender: ({ record }: { record: ipamservicev1_HostGroupMember }) => {
      return h(
        Tag,
        { color: deviceStatusToColor(record.deviceStatus) },
        { default: () => deviceStatusToName(record.deviceStatus) },
      );
    },
  },
  {
    title: $t('ipam.page.hostGroup.deviceIp'),
    dataIndex: 'devicePrimaryIp',
    width: 150,
    customRender: ({ text }: { text: string }) => {
      if (!text) return '-';
      return h('code', { class: 'bg-muted rounded px-1.5 py-0.5 font-mono text-sm' }, text);
    },
  },
  ...(isReadonly.value
    ? []
    : [
        {
          title: $t('ui.table.action'),
          dataIndex: 'action',
          width: 80,
          fixed: 'right' as const,
        },
      ]),
]);

const [Drawer, drawerApi] = useVbenDrawer({
  onCancel() {
    drawerApi.close();
  },
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      // Load data when drawer opens
      const newData = drawerApi.getData<DrawerData>();
      if (newData) {
        data.value = newData;

        if (newData.mode === 'create') {
          resetForm();
        } else if (newData.row) {
          formState.value = {
            name: newData.row.name ?? '',
            description: newData.row.description ?? '',
            status: newData.row.status ?? 'HOST_GROUP_STATUS_ACTIVE',
          };

          if (newData.row.id) {
            await loadMembers(newData.row.id);
          }
        }
      }
    } else {
      resetForm();
    }
  },
});

function resetForm() {
  formState.value = {
    name: '',
    description: '',
    status: 'HOST_GROUP_STATUS_ACTIVE',
  };
  members.value = [];
  selectedDeviceId.value = '';
  deviceSuggestions.value = [];
}

async function loadMembers(groupId: string) {
  membersLoading.value = true;
  try {
    const resp = await hostGroupStore.listMembers(groupId);
    members.value = resp.items ?? [];
  } catch (e) {
    console.error('Failed to load members:', e);
  } finally {
    membersLoading.value = false;
  }
}

// Data initialization is handled in onOpenChange

async function handleSubmit() {
  if (!formState.value.name) {
    notification.warning({ message: $t('ui.notification.required_field_missing') });
    return;
  }

  loading.value = true;
  try {
    if (data.value?.mode === 'create') {
      // Collect device IDs from members for create
      const deviceIds = members.value.map((m) => m.deviceId).filter(Boolean) as string[];
      await hostGroupStore.createGroup(
        userStore.tenantId as number,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          status: formState.value.status as any,
        },
        deviceIds,
      );
      notification.success({ message: $t('ipam.page.hostGroup.createSuccess') });
    } else if (data.value?.mode === 'edit' && data.value?.row?.id) {
      const updateMask: string[] = [];
      const original = data.value.row;

      if (formState.value.name !== original.name) updateMask.push('name');
      if (formState.value.description !== (original.description ?? ''))
        updateMask.push('description');
      if (formState.value.status !== original.status) updateMask.push('status');

      if (updateMask.length > 0) {
        await hostGroupStore.updateGroup(
          data.value.row.id,
          {
            name: formState.value.name,
            description: formState.value.description || undefined,
            status: formState.value.status as any,
          },
          updateMask,
        );
      }
      notification.success({ message: $t('ipam.page.hostGroup.updateSuccess') });
    }
    drawerApi.close();
  } catch (e: any) {
    notification.error({ message: e?.message || String(e) });
  } finally {
    loading.value = false;
  }
}

function handleCancel() {
  drawerApi.close();
}

// Member management
async function handleAddMember() {
  if (!selectedDeviceId.value) {
    notification.warning({ message: $t('ipam.page.hostGroup.selectDevice') });
    return;
  }

  // Check for duplicates
  const isDuplicate = members.value.some((m) => m.deviceId === selectedDeviceId.value);
  if (isDuplicate) {
    notification.warning({ message: $t('ipam.page.hostGroup.deviceDuplicate') });
    return;
  }

  if (isEdit.value && data.value?.row?.id) {
    // Add member to existing group via API
    try {
      const resp = await hostGroupStore.addMember(
        data.value.row.id,
        selectedDeviceId.value,
        members.value.length,
      );
      if (resp.member) {
        members.value.push(resp.member);
      }
      notification.success({ message: $t('ipam.page.hostGroup.memberAdded') });
    } catch (e: any) {
      notification.error({ message: e?.message || String(e) });
    }
  } else {
    // Add to local list (for create mode) - find device info from suggestions
    const suggestion = deviceSuggestions.value.find(
      (s) => s.value === selectedDeviceId.value,
    );
    members.value.push({
      id: `temp-${Date.now()}`,
      deviceId: selectedDeviceId.value,
      deviceName: suggestion?.device?.name ?? 'Unknown',
      deviceType: suggestion?.device?.deviceType,
      deviceStatus: suggestion?.device?.status,
      devicePrimaryIp: suggestion?.device?.primaryIp,
      sequence: members.value.length,
    } as ipamservicev1_HostGroupMember);
  }

  // Reset selection
  selectedDeviceId.value = '';
  deviceSuggestions.value = [];
}

async function handleRemoveMember(member: ipamservicev1_HostGroupMember) {
  if (isEdit.value && data.value?.row?.id && member.id && !member.id.startsWith('temp-')) {
    // Remove via API
    try {
      await hostGroupStore.removeMember(data.value.row.id, member.id);
      members.value = members.value.filter((m) => m.id !== member.id);
      notification.success({ message: $t('ipam.page.hostGroup.memberRemoved') });
    } catch (e: any) {
      notification.error({ message: e?.message || String(e) });
    }
  } else {
    // Remove from local list
    members.value = members.value.filter((m) => m.id !== member.id);
  }
}
</script>

<template>
  <Drawer :title="drawerTitle" size="lg">
    <Form layout="vertical">
      <FormItem :label="$t('ipam.page.hostGroup.name')" required>
        <Input
          v-model:value="formState.name"
          :placeholder="$t('ui.placeholder.input')"
          :disabled="isReadonly"
        />
      </FormItem>

      <FormItem :label="$t('ipam.page.hostGroup.description')">
        <Textarea
          v-model:value="formState.description"
          :placeholder="$t('ui.placeholder.input')"
          :disabled="isReadonly"
          :rows="3"
        />
      </FormItem>

      <FormItem :label="$t('ipam.page.hostGroup.status')">
        <Select
          v-model:value="formState.status"
          :options="statusOptions"
          :disabled="isReadonly"
        />
      </FormItem>

      <div class="border-border mt-6 border-t pt-4">
        <h4 class="mb-4 text-base font-medium">{{ $t('ipam.page.hostGroup.members') }}</h4>

        <!-- Add member form -->
        <div v-if="!isReadonly" class="bg-muted mb-4 rounded p-4">
          <div class="text-muted-foreground mb-3 text-xs">
            {{ $t('ipam.page.hostGroup.addMemberHint') }}
          </div>
          <div class="flex gap-2">
            <AutoComplete
              v-model:value="selectedDeviceId"
              :options="deviceSuggestions"
              :placeholder="$t('ipam.page.hostGroup.searchDevice')"
              style="flex: 1"
              @search="handleDeviceSearch"
              @select="handleSelectDevice"
              :filter-option="false"
            >
              <template #option="{ label }">
                <div class="flex items-center gap-2">
                  <LucideServer class="size-4 text-muted-foreground" />
                  <span>{{ label }}</span>
                </div>
              </template>
            </AutoComplete>
            <Button type="primary" @click="handleAddMember">
              {{ $t('ipam.page.hostGroup.addDevice') }}
            </Button>
          </div>
        </div>

        <!-- Members table -->
        <Table
          :columns="memberColumns"
          :data-source="members"
          :loading="membersLoading"
          :pagination="false"
          size="small"
          row-key="id"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.dataIndex === 'action' && !isReadonly">
              <a-popconfirm
                :cancel-text="$t('ui.button.cancel')"
                :ok-text="$t('ui.button.ok')"
                :title="$t('ipam.page.hostGroup.confirmRemoveMember')"
                @confirm="handleRemoveMember(record)"
              >
                <Button danger type="link" size="small" :icon="h(LucideTrash)" />
              </a-popconfirm>
            </template>
          </template>
          <template #emptyText>
            <div class="text-muted-foreground py-8">
              {{ $t('ipam.page.hostGroup.noMembers') }}
            </div>
          </template>
        </Table>
      </div>
    </Form>

    <template #footer>
      <Space>
        <Button @click="handleCancel">
          {{ isReadonly ? $t('ui.button.close') : $t('ui.button.cancel') }}
        </Button>
        <Button v-if="!isReadonly" type="primary" :loading="loading" @click="handleSubmit">
          {{ $t('ui.button.save') }}
        </Button>
      </Space>
    </template>
  </Drawer>
</template>
