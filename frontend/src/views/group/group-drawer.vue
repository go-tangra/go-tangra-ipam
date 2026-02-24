<script lang="ts" setup>
import { ref, computed, h, watch } from 'vue';

import { useVbenDrawer } from 'shell/vben/common-ui';
import { useUserStore } from 'shell/vben/stores';
import { LucideTrash, LucideNetwork, LucideHash, LucideGitBranch } from 'shell/vben/icons';

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
  type ipamservicev1_IpGroup,
  type ipamservicev1_IpGroupMember,
  type ipamservicev1_IpGroupMemberType,
} from '../../api/proto-types';
import { $t } from 'shell/locales';
import { useIpamGroupStore } from '../../stores/ipam-group.state';
import { useIpamIpAddressStore } from '../../stores/ipam-ip-address.state';
import { useIpamSubnetStore } from '../../stores/ipam-subnet.state';

const groupStore = useIpamGroupStore();
const ipAddressStore = useIpamIpAddressStore();
const subnetStore = useIpamSubnetStore();
const userStore = useUserStore();

interface DrawerData {
  mode: 'create' | 'edit' | 'view';
  row?: ipamservicev1_IpGroup;
}

const data = ref<DrawerData>();
const loading = ref(false);
const membersLoading = ref(false);

// Form state
const formState = ref({
  name: '',
  description: '',
  status: 'IP_GROUP_STATUS_ACTIVE' as ipamservicev1_IpGroupMemberType | string,
});

// Members state
const members = ref<ipamservicev1_IpGroupMember[]>([]);
const newMember = ref({
  memberType: 'IP_GROUP_MEMBER_TYPE_ADDRESS' as ipamservicev1_IpGroupMemberType,
  value: '',
  description: '',
});

// Autocomplete suggestions
const suggestions = ref<{ value: string; label: string }[]>([]);
const searchLoading = ref(false);
let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null;

async function handleSearch(searchText: string) {
  if (!searchText || searchText.length < 2) {
    suggestions.value = [];
    return;
  }

  // Debounce the search
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer);
  }

  searchDebounceTimer = setTimeout(async () => {
    searchLoading.value = true;
    try {
      const memberType = newMember.value.memberType;

      if (memberType === 'IP_GROUP_MEMBER_TYPE_ADDRESS') {
        // Search IP addresses
        const resp = await ipAddressStore.listIpAddresses(
          { page: 1, pageSize: 10 },
          { query: searchText },
        );
        suggestions.value = (resp.items ?? []).map((ip) => ({
          value: ip.address ?? '',
          label: ip.hostname ? `${ip.address} (${ip.hostname})` : ip.address ?? '',
        }));
      } else if (memberType === 'IP_GROUP_MEMBER_TYPE_SUBNET') {
        // Search subnets
        const resp = await subnetStore.listSubnets(
          { page: 1, pageSize: 10 },
          { query: searchText },
        );
        suggestions.value = (resp.items ?? []).map((subnet) => ({
          value: subnet.cidr ?? '',
          label: subnet.name ? `${subnet.cidr} (${subnet.name})` : subnet.cidr ?? '',
        }));
      } else {
        // For ranges, no autocomplete - clear suggestions
        suggestions.value = [];
      }
    } catch (e) {
      console.error('Failed to fetch suggestions:', e);
      suggestions.value = [];
    } finally {
      searchLoading.value = false;
    }
  }, 300);
}

function handleSelectSuggestion(value: string | number | { value: string | number; label?: string }) {
  const actualValue = typeof value === 'object' && value !== null ? value.value : value;
  newMember.value.value = String(actualValue);
}

// Clear suggestions when member type changes
watch(() => newMember.value.memberType, () => {
  suggestions.value = [];
  newMember.value.value = '';
});

const statusOptions = computed(() => [
  { value: 'IP_GROUP_STATUS_ACTIVE', label: $t('ipam.enum.groupStatus.active') },
  { value: 'IP_GROUP_STATUS_INACTIVE', label: $t('ipam.enum.groupStatus.inactive') },
]);

const memberTypeOptions = computed(() => [
  { value: 'IP_GROUP_MEMBER_TYPE_ADDRESS', label: $t('ipam.enum.memberType.address') },
  { value: 'IP_GROUP_MEMBER_TYPE_RANGE', label: $t('ipam.enum.memberType.range') },
  { value: 'IP_GROUP_MEMBER_TYPE_SUBNET', label: $t('ipam.enum.memberType.subnet') },
]);

function memberTypeToName(type: string | undefined) {
  const option = memberTypeOptions.value.find((o) => o.value === type);
  return option?.label ?? type ?? '';
}

function memberTypeToIcon(type: string | undefined) {
  switch (type) {
    case 'IP_GROUP_MEMBER_TYPE_ADDRESS':
      return h(LucideHash, { class: 'size-4' });
    case 'IP_GROUP_MEMBER_TYPE_RANGE':
      return h(LucideGitBranch, { class: 'size-4' });
    case 'IP_GROUP_MEMBER_TYPE_SUBNET':
      return h(LucideNetwork, { class: 'size-4' });
    default:
      return null;
  }
}

function memberTypeToColor(type: string | undefined) {
  switch (type) {
    case 'IP_GROUP_MEMBER_TYPE_ADDRESS':
      return 'blue';
    case 'IP_GROUP_MEMBER_TYPE_RANGE':
      return 'green';
    case 'IP_GROUP_MEMBER_TYPE_SUBNET':
      return 'purple';
    default:
      return 'default';
  }
}

const isReadonly = computed(() => data.value?.mode === 'view');
const isEdit = computed(() => data.value?.mode === 'edit');
const drawerTitle = computed(() => {
  switch (data.value?.mode) {
    case 'create':
      return $t('ipam.page.group.create');
    case 'edit':
      return $t('ipam.page.group.edit');
    case 'view':
      return $t('ipam.page.group.view');
    default:
      return '';
  }
});

const memberColumns = [
  {
    title: $t('ipam.page.group.memberType'),
    dataIndex: 'memberType',
    width: 120,
    customRender: ({ record }: { record: ipamservicev1_IpGroupMember }) => {
      return h(Tag, { color: memberTypeToColor(record.memberType) }, {
        default: () => [memberTypeToIcon(record.memberType), ' ', memberTypeToName(record.memberType)],
      });
    },
  },
  {
    title: $t('ipam.page.group.memberValue'),
    dataIndex: 'value',
    customRender: ({ text }: { text: string }) => {
      return h('code', { class: 'bg-muted rounded px-1.5 py-0.5 font-mono text-sm' }, text);
    },
  },
  {
    title: $t('ipam.page.group.memberDescription'),
    dataIndex: 'description',
    width: 200,
  },
  ...(isReadonly.value ? [] : [{
    title: $t('ui.table.action'),
    dataIndex: 'action',
    width: 80,
    fixed: 'right' as const,
  }]),
];

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
            status: newData.row.status ?? 'IP_GROUP_STATUS_ACTIVE',
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
    status: 'IP_GROUP_STATUS_ACTIVE',
  };
  members.value = [];
  newMember.value = {
    memberType: 'IP_GROUP_MEMBER_TYPE_ADDRESS',
    value: '',
    description: '',
  };
}

async function loadMembers(groupId: string) {
  membersLoading.value = true;
  try {
    const resp = await groupStore.listMembers(groupId);
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
      await groupStore.createGroup(
        userStore.tenantId as number,
        {
          name: formState.value.name,
          description: formState.value.description || undefined,
          status: formState.value.status as any,
        },
        members.value.map((m, idx) => ({
          memberType: m.memberType as ipamservicev1_IpGroupMemberType,
          value: m.value ?? '',
          description: m.description,
          sequence: idx,
        })),
      );
      notification.success({ message: $t('ipam.page.group.createSuccess') });
    } else if (data.value?.mode === 'edit' && data.value?.row?.id) {
      const updateMask: string[] = [];
      const original = data.value.row;

      if (formState.value.name !== original.name) updateMask.push('name');
      if (formState.value.description !== (original.description ?? ''))
        updateMask.push('description');
      if (formState.value.status !== original.status) updateMask.push('status');

      if (updateMask.length > 0) {
        await groupStore.updateGroup(
          data.value.row.id,
          {
            name: formState.value.name,
            description: formState.value.description || undefined,
            status: formState.value.status as any,
          },
          updateMask,
        );
      }
      notification.success({ message: $t('ipam.page.group.updateSuccess') });
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
  if (!newMember.value.value) {
    notification.warning({ message: $t('ipam.page.group.memberValueRequired') });
    return;
  }

  // Validate member format
  const validationError = validateMemberValue(
    newMember.value.memberType,
    newMember.value.value,
  );
  if (validationError) {
    notification.error({ message: validationError });
    return;
  }

  // Check for duplicates
  const isDuplicate = members.value.some(
    (m) => m.value === newMember.value.value,
  );
  if (isDuplicate) {
    notification.warning({ message: $t('ipam.page.group.memberDuplicate') });
    return;
  }

  if (isEdit.value && data.value?.row?.id) {
    // Add member to existing group via API
    try {
      const resp = await groupStore.addMember(
        data.value.row.id,
        {
          memberType: newMember.value.memberType,
          value: newMember.value.value,
          description: newMember.value.description || undefined,
          sequence: members.value.length,
        },
      );
      if (resp.member) {
        members.value.push(resp.member);
      }
      notification.success({ message: $t('ipam.page.group.memberAdded') });
    } catch (e: any) {
      notification.error({ message: e?.message || String(e) });
    }
  } else {
    // Add to local list (for create mode)
    members.value.push({
      id: `temp-${Date.now()}`,
      memberType: newMember.value.memberType,
      value: newMember.value.value,
      description: newMember.value.description,
      sequence: members.value.length,
    } as ipamservicev1_IpGroupMember);
  }

  // Reset new member form
  newMember.value = {
    memberType: 'IP_GROUP_MEMBER_TYPE_ADDRESS',
    value: '',
    description: '',
  };
}

async function handleRemoveMember(member: ipamservicev1_IpGroupMember) {
  if (isEdit.value && data.value?.row?.id && member.id && !member.id.startsWith('temp-')) {
    // Remove via API
    try {
      await groupStore.removeMember(data.value.row.id, member.id);
      members.value = members.value.filter((m) => m.id !== member.id);
      notification.success({ message: $t('ipam.page.group.memberRemoved') });
    } catch (e: any) {
      notification.error({ message: e?.message || String(e) });
    }
  } else {
    // Remove from local list
    members.value = members.value.filter((m) => m.id !== member.id);
  }
}

function validateMemberValue(
  memberType: ipamservicev1_IpGroupMemberType,
  value: string,
): string | null {
  switch (memberType) {
    case 'IP_GROUP_MEMBER_TYPE_ADDRESS': {
      // Simple IPv4/IPv6 validation
      const ipv4Regex = /^(?:\d{1,3}\.){3}\d{1,3}$/;
      const ipv6Regex = /^(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$|^::(?:[0-9a-fA-F]{1,4}:){0,6}[0-9a-fA-F]{1,4}$|^(?:[0-9a-fA-F]{1,4}:){1,7}:$/;
      if (!ipv4Regex.test(value) && !ipv6Regex.test(value)) {
        return $t('ipam.page.group.invalidIpAddress');
      }
      break;
    }
    case 'IP_GROUP_MEMBER_TYPE_RANGE': {
      const parts = value.split('-');
      if (parts.length !== 2) {
        return $t('ipam.page.group.invalidIpRange');
      }
      break;
    }
    case 'IP_GROUP_MEMBER_TYPE_SUBNET': {
      if (!value.includes('/')) {
        return $t('ipam.page.group.invalidSubnet');
      }
      break;
    }
  }
  return null;
}

function getValuePlaceholder(memberType: ipamservicev1_IpGroupMemberType) {
  switch (memberType) {
    case 'IP_GROUP_MEMBER_TYPE_ADDRESS':
      return '192.168.1.1';
    case 'IP_GROUP_MEMBER_TYPE_RANGE':
      return '192.168.1.10-192.168.1.20';
    case 'IP_GROUP_MEMBER_TYPE_SUBNET':
      return '10.0.0.0/24';
    default:
      return '';
  }
}
</script>

<template>
  <Drawer :title="drawerTitle" size="lg">
    <Form layout="vertical">
      <FormItem :label="$t('ipam.page.group.name')" required>
        <Input
          v-model:value="formState.name"
          :placeholder="$t('ui.placeholder.input')"
          :disabled="isReadonly"
        />
      </FormItem>

      <FormItem :label="$t('ipam.page.group.description')">
        <Textarea
          v-model:value="formState.description"
          :placeholder="$t('ui.placeholder.input')"
          :disabled="isReadonly"
          :rows="3"
        />
      </FormItem>

      <FormItem :label="$t('ipam.page.group.status')">
        <Select
          v-model:value="formState.status"
          :options="statusOptions"
          :disabled="isReadonly"
        />
      </FormItem>

      <div class="border-border mt-6 border-t pt-4">
        <h4 class="mb-4 text-base font-medium">{{ $t('ipam.page.group.members') }}</h4>

        <!-- Add member form -->
        <div v-if="!isReadonly" class="bg-muted mb-4 rounded p-4">
          <div class="text-muted-foreground mb-3 text-xs">
            {{ $t('ipam.page.group.memberFormatHint') }}
          </div>
          <div class="flex gap-2">
            <Select
              v-model:value="newMember.memberType"
              :options="memberTypeOptions"
              style="width: 140px"
            />
            <AutoComplete
              v-model:value="newMember.value"
              :options="suggestions"
              :placeholder="getValuePlaceholder(newMember.memberType)"
              style="flex: 1"
              class="font-mono"
              @search="handleSearch"
              @select="handleSelectSuggestion"
              :filter-option="false"
            />
          </div>
          <div class="mt-2 flex gap-2">
            <Input
              v-model:value="newMember.description"
              :placeholder="$t('ipam.page.group.memberDescriptionPlaceholder')"
              style="flex: 1"
            />
            <Button type="primary"  @click="handleAddMember">
              {{ $t('ipam.page.group.addMember') }}
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
                :title="$t('ipam.page.group.confirmRemoveMember')"
                @confirm="handleRemoveMember(record)"
              >
                <Button danger type="link" size="small" :icon="h(LucideTrash)" />
              </a-popconfirm>
            </template>
          </template>
          <template #emptyText>
            <div class="text-muted-foreground py-8">
              {{ $t('ipam.page.group.noMembers') }}
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
