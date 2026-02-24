import { defineStore } from 'pinia';

import {
  IpAddressService,
  type IpAddress,
  type IpAddressStatus,
  type ListIpAddressesResponse,
  type GetIpAddressResponse,
  type CreateIpAddressResponse,
  type UpdateIpAddressResponse,
  type SuggestAvailableAddressesResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamIpAddressStore = defineStore('ipam-ip-address', () => {
  /**
   * List IP addresses
   */
  async function listIpAddresses(
    paging?: Paging,
    formValues?: {
      subnetId?: string;
      deviceId?: string;
      status?: IpAddressStatus;
      query?: string;
    } | null,
    orderBy?: string[],
  ): Promise<ListIpAddressesResponse> {
    return await IpAddressService.list({
      subnetId: formValues?.subnetId,
      deviceId: formValues?.deviceId,
      status: formValues?.status,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
      orderBy,
    });
  }

  /**
   * Get an IP address by ID
   */
  async function getIpAddress(id: string): Promise<GetIpAddressResponse> {
    return await IpAddressService.get(id);
  }

  /**
   * Create a new IP address
   */
  async function createIpAddress(
    tenantId: number,
    data: Partial<IpAddress>,
  ): Promise<CreateIpAddressResponse> {
    return await IpAddressService.create({
      tenantId,
      address: data.address!,
      subnetId: data.subnetId!,
      hostname: data.hostname,
      macAddress: data.macAddress,
      description: data.description,
      deviceId: data.deviceId,
      status: data.status,
    });
  }

  /**
   * Update an IP address
   */
  async function updateIpAddress(
    id: string,
    data: Partial<IpAddress>,
    updateMask: string[],
  ): Promise<UpdateIpAddressResponse> {
    return await IpAddressService.update(id, {
      id,
      data: data as IpAddress,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete an IP address
   */
  async function deleteIpAddress(id: string): Promise<void> {
    return await IpAddressService.delete(id);
  }

  /**
   * Allocate the next available IP address from a subnet
   */
  async function allocateNextAddress(
    tenantId: number,
    subnetId: string,
    data?: Partial<IpAddress>,
  ) {
    return await IpAddressService.allocateNext({
      tenantId,
      subnetId,
      hostname: data?.hostname,
      description: data?.description,
      deviceId: data?.deviceId,
    });
  }

  /**
   * Suggest available IP addresses in a subnet (verified via ICMP ping + TCP port scan)
   */
  async function suggestAvailableAddresses(
    subnetId: string,
    count?: number,
    skipAddresses?: string[],
  ): Promise<SuggestAvailableAddressesResponse> {
    return await IpAddressService.suggestAvailable({
      subnetId,
      count,
      skipAddresses,
    });
  }

  function $reset() {}

  return {
    $reset,
    listIpAddresses,
    getIpAddress,
    createIpAddress,
    updateIpAddress,
    deleteIpAddress,
    allocateNextAddress,
    suggestAvailableAddresses,
  };
});
