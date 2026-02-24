/**
 * IPAM Module Service Functions
 *
 * Typed service methods for the IPAM API using dynamic module routing.
 * Base URL: /admin/v1/modules/ipam/v1
 */

import type { components, operations } from './types';
import { ipamApi, type RequestOptions } from './client';

// Entity type aliases
export type Subnet = components['schemas']['Subnet'];
export type IpAddress = components['schemas']['IpAddress'];
export type Vlan = components['schemas']['Vlan'];
export type Device = components['schemas']['Device'];
export type Location = components['schemas']['Location'];
export type IpScanJob = components['schemas']['IpScanJob'];
export type IpGroup = components['schemas']['IpGroup'];
export type IpGroupMember = components['schemas']['IpGroupMember'];
export type HostGroup = components['schemas']['HostGroup'];
export type HostGroupMember = components['schemas']['HostGroupMember'];
export type DeviceInterface = components['schemas']['DeviceInterface'];
export type DnsConfig = components['schemas']['DnsConfig'];
export type ScanConfig = components['schemas']['ScanConfig'];

// Enum types derived from entity fields
export type SubnetStatus = NonNullable<Subnet['status']>;
export type IpAddressStatus = NonNullable<IpAddress['status']>;
export type IpAddressType = NonNullable<IpAddress['addressType']>;
export type DeviceStatus = NonNullable<Device['status']>;
export type DeviceType = NonNullable<Device['deviceType']>;
export type LocationStatus = NonNullable<Location['status']>;
export type LocationType = NonNullable<Location['locationType']>;
export type VlanStatus = NonNullable<Vlan['status']>;
export type IpScanJobStatus = NonNullable<IpScanJob['status']>;
export type IpGroupStatus = NonNullable<IpGroup['status']>;
export type IpGroupMemberType = NonNullable<IpGroupMember['memberType']>;
export type HostGroupStatus = NonNullable<HostGroup['status']>;

// List Response types
export type ListSubnetsResponse = components['schemas']['ListSubnetsResponse'];
export type ListIpAddressesResponse = components['schemas']['ListIpAddressesResponse'];
export type ListVlansResponse = components['schemas']['ListVlansResponse'];
export type ListDevicesResponse = components['schemas']['ListDevicesResponse'];
export type ListLocationsResponse = components['schemas']['ListLocationsResponse'];
export type ListScanJobsResponse = components['schemas']['ListScanJobsResponse'];
export type ListIpGroupsResponse = components['schemas']['ListIpGroupsResponse'];
export type ListIpGroupMembersResponse = components['schemas']['ListIpGroupMembersResponse'];
export type ListHostGroupsResponse = components['schemas']['ListHostGroupsResponse'];
export type ListHostGroupMembersResponse = components['schemas']['ListHostGroupMembersResponse'];
export type ListDeviceHostGroupsResponse = components['schemas']['ListDeviceHostGroupsResponse'];

// Get Response types
export type GetSubnetResponse = components['schemas']['GetSubnetResponse'];
export type GetIpAddressResponse = components['schemas']['GetIpAddressResponse'];
export type GetVlanResponse = components['schemas']['GetVlanResponse'];
export type GetDeviceResponse = components['schemas']['GetDeviceResponse'];
export type GetLocationResponse = components['schemas']['GetLocationResponse'];
export type GetScanJobResponse = components['schemas']['GetScanJobResponse'];
export type GetIpGroupResponse = components['schemas']['GetIpGroupResponse'];
export type GetHostGroupResponse = components['schemas']['GetHostGroupResponse'];

// Create Response types
export type CreateSubnetResponse = components['schemas']['CreateSubnetResponse'];
export type CreateIpAddressResponse = components['schemas']['CreateIpAddressResponse'];
export type CreateVlanResponse = components['schemas']['CreateVlanResponse'];
export type CreateDeviceResponse = components['schemas']['CreateDeviceResponse'];
export type CreateLocationResponse = components['schemas']['CreateLocationResponse'];
export type CreateIpGroupResponse = components['schemas']['CreateIpGroupResponse'];
export type CreateHostGroupResponse = components['schemas']['CreateHostGroupResponse'];

// Update Response types
export type UpdateSubnetResponse = components['schemas']['UpdateSubnetResponse'];
export type UpdateIpAddressResponse = components['schemas']['UpdateIpAddressResponse'];
export type UpdateVlanResponse = components['schemas']['UpdateVlanResponse'];
export type UpdateDeviceResponse = components['schemas']['UpdateDeviceResponse'];
export type UpdateLocationResponse = components['schemas']['UpdateLocationResponse'];
export type UpdateIpGroupResponse = components['schemas']['UpdateIpGroupResponse'];
export type UpdateHostGroupResponse = components['schemas']['UpdateHostGroupResponse'];

// Other Response types
export type GetSubnetTreeResponse = components['schemas']['GetSubnetTreeResponse'];
export type GetSubnetStatsResponse = components['schemas']['GetSubnetStatsResponse'];
export type ScanSubnetResponse = components['schemas']['ScanSubnetResponse'];
export type GetLocationTreeResponse = components['schemas']['GetLocationTreeResponse'];
export type GetVlanSubnetsResponse = components['schemas']['GetVlanSubnetsResponse'];
export type GetStatsResponse = components['schemas']['GetStatsResponse'];
export type HealthCheckResponse = components['schemas']['HealthCheckResponse'];
export type GetDnsConfigResponse = components['schemas']['GetDnsConfigResponse'];
export type UpdateDnsConfigResponse = components['schemas']['UpdateDnsConfigResponse'];
export type TestDnsConfigResponse = components['schemas']['TestDnsConfigResponse'];
export type AllocateNextAddressResponse = components['schemas']['AllocateNextAddressResponse'];
export type BulkAllocateAddressesResponse = components['schemas']['BulkAllocateAddressesResponse'];
export type CheckIpInGroupResponse = components['schemas']['CheckIpInGroupResponse'];
export type AddIpGroupMemberResponse = components['schemas']['AddIpGroupMemberResponse'];
export type AddHostGroupMemberResponse = components['schemas']['AddHostGroupMemberResponse'];
export type UpdateHostGroupMemberResponse = components['schemas']['UpdateHostGroupMemberResponse'];
export type GetDeviceAddressesResponse = components['schemas']['GetDeviceAddressesResponse'];
export type GetDeviceInterfacesResponse = components['schemas']['GetDeviceInterfacesResponse'];
export type CreateDeviceInterfaceResponse = components['schemas']['CreateDeviceInterfaceResponse'];
export type CancelScanResponse = components['schemas']['CancelScanResponse'];

// Suggest available addresses types (not yet in OpenAPI schema, defined inline)
export interface SuggestedAddress {
  address: string;
  pingFree: boolean;
  portScanFree: boolean;
}

export interface SuggestAvailableAddressesResponse {
  addresses?: SuggestedAddress[];
  totalUnallocated?: number;
}

// Request types
export type CreateSubnetRequest = components['schemas']['CreateSubnetRequest'];
export type UpdateSubnetRequest = components['schemas']['UpdateSubnetRequest'];
export type CreateIpAddressRequest = components['schemas']['CreateIpAddressRequest'];
export type UpdateIpAddressRequest = components['schemas']['UpdateIpAddressRequest'];
export type CreateVlanRequest = components['schemas']['CreateVlanRequest'];
export type UpdateVlanRequest = components['schemas']['UpdateVlanRequest'];
export type CreateDeviceRequest = components['schemas']['CreateDeviceRequest'];
export type UpdateDeviceRequest = components['schemas']['UpdateDeviceRequest'];
export type CreateLocationRequest = components['schemas']['CreateLocationRequest'];
export type UpdateLocationRequest = components['schemas']['UpdateLocationRequest'];
export type CreateIpGroupRequest = components['schemas']['CreateIpGroupRequest'];
export type UpdateIpGroupRequest = components['schemas']['UpdateIpGroupRequest'];
export type CreateHostGroupRequest = components['schemas']['CreateHostGroupRequest'];
export type UpdateHostGroupRequest = components['schemas']['UpdateHostGroupRequest'];
export type AllocateNextAddressRequest = components['schemas']['AllocateNextAddressRequest'];
export type BulkAllocateAddressesRequest = components['schemas']['BulkAllocateAddressesRequest'];
export type AddIpGroupMemberRequest = components['schemas']['AddIpGroupMemberRequest'];
export type AddHostGroupMemberRequest = components['schemas']['AddHostGroupMemberRequest'];
export type CreateDeviceInterfaceRequest = components['schemas']['CreateDeviceInterfaceRequest'];
export type StartScanRequest = components['schemas']['StartScanRequest'];

// Helper to build query string
function buildQuery(params: Record<string, unknown>): string {
  const searchParams = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') {
      if (Array.isArray(value)) {
        value.forEach(v => searchParams.append(key, String(v)));
      } else {
        searchParams.append(key, String(value));
      }
    }
  }
  const query = searchParams.toString();
  return query ? `?${query}` : '';
}

// ==================== Subnet Service ====================

export const SubnetService = {
  list: async (
    params?: operations['SubnetService_ListSubnets']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListSubnetsResponse> => {
    return ipamApi.get<ListSubnetsResponse>(`/subnets${buildQuery(params || {})}`, options);
  },

  get: async (id: string, options?: RequestOptions): Promise<GetSubnetResponse> => {
    return ipamApi.get<GetSubnetResponse>(`/subnets/${id}`, options);
  },

  create: async (
    data: CreateSubnetRequest,
    options?: RequestOptions
  ): Promise<CreateSubnetResponse> => {
    return ipamApi.post<CreateSubnetResponse>('/subnets', data, options);
  },

  update: async (
    id: string,
    data: UpdateSubnetRequest,
    options?: RequestOptions
  ): Promise<UpdateSubnetResponse> => {
    return ipamApi.put<UpdateSubnetResponse>(`/subnets/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/subnets/${id}`, options);
  },

  getTree: async (
    params?: operations['SubnetService_GetSubnetTree']['parameters']['query'],
    options?: RequestOptions
  ): Promise<GetSubnetTreeResponse> => {
    return ipamApi.get<GetSubnetTreeResponse>(`/subnets/tree${buildQuery(params || {})}`, options);
  },

  getStats: async (id: string, options?: RequestOptions): Promise<GetSubnetStatsResponse> => {
    return ipamApi.get<GetSubnetStatsResponse>(`/subnets/${id}/stats`, options);
  },
};

// ==================== IP Address Service ====================

export const IpAddressService = {
  list: async (
    params?: operations['IpAddressService_ListIpAddresses']['parameters']['query'] & { orderBy?: string[] },
    options?: RequestOptions
  ): Promise<ListIpAddressesResponse> => {
    return ipamApi.get<ListIpAddressesResponse>(`/ip-addresses${buildQuery(params || {})}`, options);
  },

  get: async (id: string, options?: RequestOptions): Promise<GetIpAddressResponse> => {
    return ipamApi.get<GetIpAddressResponse>(`/ip-addresses/${id}`, options);
  },

  create: async (
    data: CreateIpAddressRequest,
    options?: RequestOptions
  ): Promise<CreateIpAddressResponse> => {
    return ipamApi.post<CreateIpAddressResponse>('/ip-addresses', data, options);
  },

  update: async (
    id: string,
    data: UpdateIpAddressRequest,
    options?: RequestOptions
  ): Promise<UpdateIpAddressResponse> => {
    return ipamApi.put<UpdateIpAddressResponse>(`/ip-addresses/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/ip-addresses/${id}`, options);
  },

  allocateNext: async (
    data: AllocateNextAddressRequest,
    options?: RequestOptions
  ): Promise<AllocateNextAddressResponse> => {
    return ipamApi.post<AllocateNextAddressResponse>('/ip-addresses/allocate', data, options);
  },

  bulkAllocate: async (
    data: BulkAllocateAddressesRequest,
    options?: RequestOptions
  ): Promise<BulkAllocateAddressesResponse> => {
    return ipamApi.post<BulkAllocateAddressesResponse>('/ip-addresses/allocate/bulk', data, options);
  },

  suggestAvailable: async (
    params: { subnetId: string; count?: number; skipAddresses?: string[] },
    options?: RequestOptions
  ): Promise<SuggestAvailableAddressesResponse> => {
    return ipamApi.get<SuggestAvailableAddressesResponse>(`/ip-addresses/suggest${buildQuery(params)}`, options);
  },
};

// ==================== VLAN Service ====================

export const VlanService = {
  list: async (
    params?: operations['VlanService_ListVlans']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListVlansResponse> => {
    return ipamApi.get<ListVlansResponse>(`/vlans${buildQuery(params || {})}`, options);
  },

  get: async (id: string, options?: RequestOptions): Promise<GetVlanResponse> => {
    return ipamApi.get<GetVlanResponse>(`/vlans/${id}`, options);
  },

  create: async (
    data: CreateVlanRequest,
    options?: RequestOptions
  ): Promise<CreateVlanResponse> => {
    return ipamApi.post<CreateVlanResponse>('/vlans', data, options);
  },

  update: async (
    id: string,
    data: UpdateVlanRequest,
    options?: RequestOptions
  ): Promise<UpdateVlanResponse> => {
    return ipamApi.put<UpdateVlanResponse>(`/vlans/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/vlans/${id}`, options);
  },

  getSubnets: async (id: string, options?: RequestOptions): Promise<GetVlanSubnetsResponse> => {
    return ipamApi.get<GetVlanSubnetsResponse>(`/vlans/${id}/subnets`, options);
  },
};

// ==================== Device Service ====================

export const DeviceService = {
  list: async (
    params?: operations['DeviceService_ListDevices']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListDevicesResponse> => {
    return ipamApi.get<ListDevicesResponse>(`/devices${buildQuery(params || {})}`, options);
  },

  get: async (id: string, options?: RequestOptions): Promise<GetDeviceResponse> => {
    return ipamApi.get<GetDeviceResponse>(`/devices/${id}`, options);
  },

  create: async (
    data: CreateDeviceRequest,
    options?: RequestOptions
  ): Promise<CreateDeviceResponse> => {
    return ipamApi.post<CreateDeviceResponse>('/devices', data, options);
  },

  update: async (
    id: string,
    data: UpdateDeviceRequest,
    options?: RequestOptions
  ): Promise<UpdateDeviceResponse> => {
    return ipamApi.put<UpdateDeviceResponse>(`/devices/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/devices/${id}`, options);
  },

  getAddresses: async (id: string, options?: RequestOptions): Promise<GetDeviceAddressesResponse> => {
    return ipamApi.get<GetDeviceAddressesResponse>(`/devices/${id}/addresses`, options);
  },

  getInterfaces: async (deviceId: string, options?: RequestOptions): Promise<GetDeviceInterfacesResponse> => {
    return ipamApi.get<GetDeviceInterfacesResponse>(`/devices/${deviceId}/interfaces`, options);
  },

  createInterface: async (
    deviceId: string,
    data: Omit<CreateDeviceInterfaceRequest, 'deviceId'>,
    options?: RequestOptions
  ): Promise<CreateDeviceInterfaceResponse> => {
    return ipamApi.post<CreateDeviceInterfaceResponse>(`/devices/${deviceId}/interfaces`, { ...data, deviceId }, options);
  },

  deleteInterface: async (deviceId: string, interfaceId: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/devices/${deviceId}/interfaces/${interfaceId}`, options);
  },
};

// ==================== Location Service ====================

export const LocationService = {
  list: async (
    params?: operations['LocationService_ListLocations']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListLocationsResponse> => {
    return ipamApi.get<ListLocationsResponse>(`/locations${buildQuery(params || {})}`, options);
  },

  get: async (id: string, options?: RequestOptions): Promise<GetLocationResponse> => {
    return ipamApi.get<GetLocationResponse>(`/locations/${id}`, options);
  },

  create: async (
    data: CreateLocationRequest,
    options?: RequestOptions
  ): Promise<CreateLocationResponse> => {
    return ipamApi.post<CreateLocationResponse>('/locations', data, options);
  },

  update: async (
    id: string,
    data: UpdateLocationRequest,
    options?: RequestOptions
  ): Promise<UpdateLocationResponse> => {
    return ipamApi.put<UpdateLocationResponse>(`/locations/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/locations/${id}`, options);
  },

  getTree: async (
    params?: operations['LocationService_GetLocationTree']['parameters']['query'],
    options?: RequestOptions
  ): Promise<GetLocationTreeResponse> => {
    return ipamApi.get<GetLocationTreeResponse>(`/locations/tree${buildQuery(params || {})}`, options);
  },
};

// ==================== IP Scan Service ====================

export const IpScanService = {
  list: async (
    params?: operations['IpScanService_ListScanJobs']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListScanJobsResponse> => {
    return ipamApi.get<ListScanJobsResponse>(`/ip-scans${buildQuery(params || {})}`, options);
  },

  get: async (id: string, options?: RequestOptions): Promise<GetScanJobResponse> => {
    return ipamApi.get<GetScanJobResponse>(`/ip-scans/${id}`, options);
  },

  start: async (
    data: StartScanRequest,
    options?: RequestOptions
  ): Promise<GetScanJobResponse> => {
    return ipamApi.post<GetScanJobResponse>('/ip-scans', data, options);
  },

  cancel: async (id: string, options?: RequestOptions): Promise<CancelScanResponse> => {
    return ipamApi.post<CancelScanResponse>(`/ip-scans/${id}/cancel`, {}, options);
  },
};

// ==================== IP Group Service ====================

export const IpGroupService = {
  list: async (
    params?: operations['IpGroupService_ListIpGroups']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListIpGroupsResponse> => {
    return ipamApi.get<ListIpGroupsResponse>(`/ip-groups${buildQuery(params || {})}`, options);
  },

  get: async (id: string, includeMembers?: boolean, options?: RequestOptions): Promise<GetIpGroupResponse> => {
    const query = includeMembers ? '?includeMembers=true' : '';
    return ipamApi.get<GetIpGroupResponse>(`/ip-groups/${id}${query}`, options);
  },

  create: async (
    data: CreateIpGroupRequest,
    options?: RequestOptions
  ): Promise<CreateIpGroupResponse> => {
    return ipamApi.post<CreateIpGroupResponse>('/ip-groups', data, options);
  },

  update: async (
    id: string,
    data: UpdateIpGroupRequest,
    options?: RequestOptions
  ): Promise<UpdateIpGroupResponse> => {
    return ipamApi.put<UpdateIpGroupResponse>(`/ip-groups/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/ip-groups/${id}`, options);
  },

  listMembers: async (
    ipGroupId: string,
    params?: { page?: number; pageSize?: number },
    options?: RequestOptions
  ): Promise<ListIpGroupMembersResponse> => {
    return ipamApi.get<ListIpGroupMembersResponse>(`/ip-groups/${ipGroupId}/members${buildQuery(params || {})}`, options);
  },

  addMember: async (
    ipGroupId: string,
    data: Omit<AddIpGroupMemberRequest, 'ipGroupId'>,
    options?: RequestOptions
  ): Promise<AddIpGroupMemberResponse> => {
    return ipamApi.post<AddIpGroupMemberResponse>(`/ip-groups/${ipGroupId}/members`, { ...data, ipGroupId }, options);
  },

  removeMember: async (ipGroupId: string, memberId: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/ip-groups/${ipGroupId}/members/${memberId}`, options);
  },

  checkIp: async (ip: string, options?: RequestOptions): Promise<CheckIpInGroupResponse> => {
    return ipamApi.get<CheckIpInGroupResponse>(`/ip-groups/check?ip=${encodeURIComponent(ip)}`, options);
  },
};

// ==================== Host Group Service ====================

export const HostGroupService = {
  list: async (
    params?: operations['HostGroupService_ListHostGroups']['parameters']['query'],
    options?: RequestOptions
  ): Promise<ListHostGroupsResponse> => {
    return ipamApi.get<ListHostGroupsResponse>(`/host-groups${buildQuery(params || {})}`, options);
  },

  get: async (id: string, includeMembers?: boolean, options?: RequestOptions): Promise<GetHostGroupResponse> => {
    const query = includeMembers ? '?includeMembers=true' : '';
    return ipamApi.get<GetHostGroupResponse>(`/host-groups/${id}${query}`, options);
  },

  create: async (
    data: CreateHostGroupRequest,
    options?: RequestOptions
  ): Promise<CreateHostGroupResponse> => {
    return ipamApi.post<CreateHostGroupResponse>('/host-groups', data, options);
  },

  update: async (
    id: string,
    data: UpdateHostGroupRequest,
    options?: RequestOptions
  ): Promise<UpdateHostGroupResponse> => {
    return ipamApi.put<UpdateHostGroupResponse>(`/host-groups/${id}`, data, options);
  },

  delete: async (id: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/host-groups/${id}`, options);
  },

  listMembers: async (
    hostGroupId: string,
    params?: { page?: number; pageSize?: number },
    options?: RequestOptions
  ): Promise<ListHostGroupMembersResponse> => {
    return ipamApi.get<ListHostGroupMembersResponse>(`/host-groups/${hostGroupId}/members${buildQuery(params || {})}`, options);
  },

  addMember: async (
    hostGroupId: string,
    deviceId: string,
    sequence?: number,
    options?: RequestOptions
  ): Promise<AddHostGroupMemberResponse> => {
    return ipamApi.post<AddHostGroupMemberResponse>(`/host-groups/${hostGroupId}/members`, { hostGroupId, deviceId, sequence }, options);
  },

  removeMember: async (hostGroupId: string, memberId: string, options?: RequestOptions): Promise<void> => {
    return ipamApi.delete(`/host-groups/${hostGroupId}/members/${memberId}`, options);
  },

  updateMember: async (
    hostGroupId: string,
    memberId: string,
    sequence?: number,
    options?: RequestOptions
  ): Promise<UpdateHostGroupMemberResponse> => {
    return ipamApi.patch<UpdateHostGroupMemberResponse>(`/host-groups/${hostGroupId}/members/${memberId}`, { sequence }, options);
  },

  listDeviceGroups: async (deviceId: string, options?: RequestOptions): Promise<ListDeviceHostGroupsResponse> => {
    return ipamApi.get<ListDeviceHostGroupsResponse>(`/devices/${deviceId}/host-groups`, options);
  },
};

// ==================== System Service ====================

export const SystemService = {
  healthCheck: async (options?: RequestOptions): Promise<HealthCheckResponse> => {
    return ipamApi.get<HealthCheckResponse>('/health', options);
  },

  getStats: async (
    params?: operations['SystemService_GetStats']['parameters']['query'],
    options?: RequestOptions
  ): Promise<GetStatsResponse> => {
    return ipamApi.get<GetStatsResponse>(`/stats${buildQuery(params || {})}`, options);
  },

  getDnsConfig: async (options?: RequestOptions): Promise<GetDnsConfigResponse> => {
    return ipamApi.get<GetDnsConfigResponse>('/dns-config', options);
  },

  updateDnsConfig: async (
    data: Partial<DnsConfig>,
    options?: RequestOptions
  ): Promise<UpdateDnsConfigResponse> => {
    return ipamApi.put<UpdateDnsConfigResponse>('/dns-config', data, options);
  },

  testDnsConfig: async (options?: RequestOptions): Promise<TestDnsConfigResponse> => {
    return ipamApi.post<TestDnsConfigResponse>('/dns-config/test', {}, options);
  },
};
