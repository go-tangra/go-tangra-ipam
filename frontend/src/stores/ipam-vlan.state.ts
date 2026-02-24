import { defineStore } from 'pinia';

import {
  VlanService,
  type Vlan,
  type ListVlansResponse,
  type GetVlanResponse,
  type CreateVlanResponse,
  type UpdateVlanResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

// Status type for VLAN
type VlanStatus = 'VLAN_STATUS_UNSPECIFIED' | 'VLAN_STATUS_ACTIVE' | 'VLAN_STATUS_RESERVED' | 'VLAN_STATUS_DEPRECATED';

export const useIpamVlanStore = defineStore('ipam-vlan', () => {
  /**
   * List VLANs
   */
  async function listVlans(
    paging?: Paging,
    formValues?: {
      locationId?: string;
      status?: VlanStatus;
      query?: string;
    } | null,
  ): Promise<ListVlansResponse> {
    return await VlanService.list({
      locationId: formValues?.locationId,
      status: formValues?.status,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Get a VLAN by ID
   */
  async function getVlan(id: string): Promise<GetVlanResponse> {
    return await VlanService.get(id);
  }

  /**
   * Create a new VLAN
   */
  async function createVlan(
    tenantId: number,
    data: Partial<Vlan>,
  ): Promise<CreateVlanResponse> {
    return await VlanService.create({
      tenantId,
      vlanId: data.vlanId!,
      name: data.name!,
      description: data.description,
      domain: data.domain,
      locationId: data.locationId,
      status: data.status,
    });
  }

  /**
   * Update a VLAN
   */
  async function updateVlan(
    id: string,
    data: Partial<Vlan>,
    updateMask: string[],
  ): Promise<UpdateVlanResponse> {
    return await VlanService.update(id, {
      id,
      data: data as Vlan,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete a VLAN
   */
  async function deleteVlan(id: string): Promise<void> {
    return await VlanService.delete(id);
  }

  function $reset() {}

  return {
    $reset,
    listVlans,
    getVlan,
    createVlan,
    updateVlan,
    deleteVlan,
  };
});
