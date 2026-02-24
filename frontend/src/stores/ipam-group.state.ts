import { defineStore } from 'pinia';

import {
  IpGroupService,
  type IpGroup,
  type IpGroupMemberType,
  type IpGroupStatus,
  type ListIpGroupsResponse,
  type GetIpGroupResponse,
  type CreateIpGroupResponse,
  type UpdateIpGroupResponse,
  type ListIpGroupMembersResponse,
  type AddIpGroupMemberResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamGroupStore = defineStore('ipam-group', () => {
  /**
   * List IP groups
   */
  async function listGroups(
    paging?: Paging,
    formValues?: {
      status?: IpGroupStatus;
      query?: string;
    } | null,
  ): Promise<ListIpGroupsResponse> {
    return await IpGroupService.list({
      status: formValues?.status,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Get an IP group by ID
   */
  async function getGroup(
    id: string,
    includeMembers?: boolean,
  ): Promise<GetIpGroupResponse> {
    return await IpGroupService.get(id, includeMembers);
  }

  /**
   * Create a new IP group
   */
  async function createGroup(
    tenantId: number,
    data: Partial<IpGroup>,
    members?: Array<{
      memberType: IpGroupMemberType;
      value: string;
      description?: string;
      sequence?: number;
    }>,
  ): Promise<CreateIpGroupResponse> {
    return await IpGroupService.create({
      tenantId,
      name: data.name!,
      description: data.description,
      status: data.status,
      tags: data.tags,
      metadata: data.metadata,
      members: members?.map((m) => ({
        memberType: m.memberType,
        value: m.value,
        description: m.description,
        sequence: m.sequence,
      })),
    });
  }

  /**
   * Update an IP group
   */
  async function updateGroup(
    id: string,
    data: Partial<IpGroup>,
    updateMask: string[],
  ): Promise<UpdateIpGroupResponse> {
    return await IpGroupService.update(id, {
      id,
      data: data as IpGroup,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete an IP group
   */
  async function deleteGroup(id: string): Promise<void> {
    return await IpGroupService.delete(id);
  }

  /**
   * List members of an IP group
   */
  async function listMembers(
    ipGroupId: string,
    paging?: Paging,
  ): Promise<ListIpGroupMembersResponse> {
    return await IpGroupService.listMembers(ipGroupId, {
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Add a member to an IP group
   */
  async function addMember(
    ipGroupId: string,
    member: {
      memberType: IpGroupMemberType;
      value: string;
      description?: string;
      sequence?: number;
    },
  ): Promise<AddIpGroupMemberResponse> {
    return await IpGroupService.addMember(ipGroupId, member);
  }

  /**
   * Remove a member from an IP group
   */
  async function removeMember(
    ipGroupId: string,
    memberId: string,
  ): Promise<void> {
    return await IpGroupService.removeMember(ipGroupId, memberId);
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
