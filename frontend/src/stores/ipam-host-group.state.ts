import { defineStore } from 'pinia';

import {
  HostGroupService,
  type HostGroup,
  type HostGroupStatus,
  type ListHostGroupsResponse,
  type GetHostGroupResponse,
  type CreateHostGroupResponse,
  type UpdateHostGroupResponse,
  type ListHostGroupMembersResponse,
  type AddHostGroupMemberResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamHostGroupStore = defineStore('ipam-host-group', () => {
  /**
   * List host groups
   */
  async function listGroups(
    paging?: Paging,
    formValues?: {
      status?: HostGroupStatus;
      query?: string;
    } | null,
  ): Promise<ListHostGroupsResponse> {
    return await HostGroupService.list({
      status: formValues?.status,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Get a host group by ID
   */
  async function getGroup(
    id: string,
    includeMembers?: boolean,
  ): Promise<GetHostGroupResponse> {
    return await HostGroupService.get(id, includeMembers);
  }

  /**
   * Create a new host group
   */
  async function createGroup(
    tenantId: number,
    data: Partial<HostGroup>,
    deviceIds?: string[],
  ): Promise<CreateHostGroupResponse> {
    return await HostGroupService.create({
      tenantId,
      name: data.name!,
      description: data.description,
      status: data.status,
      tags: data.tags,
      metadata: data.metadata,
      deviceIds,
    });
  }

  /**
   * Update a host group
   */
  async function updateGroup(
    id: string,
    data: Partial<HostGroup>,
    updateMask: string[],
  ): Promise<UpdateHostGroupResponse> {
    return await HostGroupService.update(id, {
      id,
      data: data as HostGroup,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete a host group
   */
  async function deleteGroup(id: string): Promise<void> {
    return await HostGroupService.delete(id);
  }

  /**
   * List members of a host group
   */
  async function listMembers(
    hostGroupId: string,
    paging?: Paging,
  ): Promise<ListHostGroupMembersResponse> {
    return await HostGroupService.listMembers(hostGroupId, {
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Add a member to a host group
   */
  async function addMember(
    hostGroupId: string,
    deviceId: string,
    sequence?: number,
  ): Promise<AddHostGroupMemberResponse> {
    return await HostGroupService.addMember(hostGroupId, deviceId, sequence);
  }

  /**
   * Remove a member from a host group
   */
  async function removeMember(
    hostGroupId: string,
    memberId: string,
  ): Promise<void> {
    return await HostGroupService.removeMember(hostGroupId, memberId);
  }

  function $reset() {}

  return {
    $reset,
    listGroups,
    getGroup,
    createGroup,
    updateGroup,
    deleteGroup,
    listMembers,
    addMember,
    removeMember,
  };
});
