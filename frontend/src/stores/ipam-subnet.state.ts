import { defineStore } from 'pinia';

import {
  SubnetService,
  IpScanService,
  type Subnet,
  type ListSubnetsResponse,
  type GetSubnetResponse,
  type CreateSubnetResponse,
  type UpdateSubnetResponse,
  type GetSubnetTreeResponse,
  type GetSubnetStatsResponse,
  type GetScanJobResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamSubnetStore = defineStore('ipam-subnet', () => {
  /**
   * List subnets
   */
  async function listSubnets(
    paging?: Paging,
    formValues?: {
      parentId?: string;
      vlanId?: string;
      locationId?: string;
      query?: string;
    } | null,
  ): Promise<ListSubnetsResponse> {
    return await SubnetService.list({
      parentId: formValues?.parentId,
      vlanId: formValues?.vlanId,
      locationId: formValues?.locationId,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Get a subnet by ID
   */
  async function getSubnet(id: string): Promise<GetSubnetResponse> {
    return await SubnetService.get(id);
  }

  /**
   * Create a new subnet
   */
  async function createSubnet(
    tenantId: number,
    data: Partial<Subnet>,
    autoScan?: boolean,
  ): Promise<CreateSubnetResponse> {
    return await SubnetService.create({
      tenantId,
      name: data.name!,
      cidr: data.cidr!,
      description: data.description,
      gateway: data.gateway,
      dnsServers: data.dnsServers,
      vlanId: data.vlanId,
      parentId: data.parentId,
      locationId: data.locationId,
      snmpVersion: data.snmpVersion,
      snmpCommunity: data.snmpCommunity,
      snmpUser: data.snmpUser,
      snmpAuthPassword: data.snmpAuthPassword,
      snmpPrivPassword: data.snmpPrivPassword,
      snmpAuthProtocol: data.snmpAuthProtocol,
      snmpPrivProtocol: data.snmpPrivProtocol,
      autoScan,
    });
  }

  /**
   * Update a subnet
   */
  async function updateSubnet(
    id: string,
    data: Partial<Subnet>,
    updateMask: string[],
  ): Promise<UpdateSubnetResponse> {
    return await SubnetService.update(id, {
      id,
      data: data as Subnet,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete a subnet
   */
  async function deleteSubnet(id: string): Promise<void> {
    return await SubnetService.delete(id);
  }

  /**
   * Get subnet tree structure
   */
  async function getSubnetTree(
    rootId?: string,
    maxDepth?: number,
  ): Promise<GetSubnetTreeResponse> {
    return await SubnetService.getTree({ rootId, maxDepth });
  }

  /**
   * Get subnet statistics
   */
  async function getSubnetStats(id: string): Promise<GetSubnetStatsResponse> {
    return await SubnetService.getStats(id);
  }

  /**
   * Scan subnet for active hosts
   */
  async function scanSubnet(subnetId: string, tenantId: number): Promise<GetScanJobResponse> {
    return await IpScanService.start({ tenantId, subnetId });
  }

  function $reset() {}

  return {
    $reset,
    listSubnets,
    getSubnet,
    createSubnet,
    updateSubnet,
    deleteSubnet,
    getSubnetTree,
    getSubnetStats,
    scanSubnet,
  };
});
