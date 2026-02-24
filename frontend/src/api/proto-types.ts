/**
 * Proto-compatible type aliases
 *
 * In the admin SPA, these types were imported from the proto-generated
 * admin service types (#/generated/api/admin/service/v1). In the MF remote,
 * we re-export them from the OpenAPI-generated types with matching names.
 */

import type { components } from './types';

// Entity types (matching ipamservicev1_* naming from proto)
export type ipamservicev1_Subnet = components['schemas']['Subnet'];
export type ipamservicev1_IpAddress = components['schemas']['IpAddress'];
export type ipamservicev1_Vlan = components['schemas']['Vlan'];
export type ipamservicev1_Device = components['schemas']['Device'];
export type ipamservicev1_Location = components['schemas']['Location'];
export type ipamservicev1_IpScanJob = components['schemas']['IpScanJob'];
export type ipamservicev1_IpGroup = components['schemas']['IpGroup'];
export type ipamservicev1_IpGroupMember = components['schemas']['IpGroupMember'];
export type ipamservicev1_HostGroup = components['schemas']['HostGroup'];
export type ipamservicev1_HostGroupMember = components['schemas']['HostGroupMember'];

// Enum types
export type ipamservicev1_IpGroupMemberType = NonNullable<components['schemas']['IpGroupMember']['memberType']>;

// Tree node types (these come from the response schemas)
export type ipamservicev1_SubnetTreeNode = NonNullable<components['schemas']['GetSubnetTreeResponse']['nodes']>[number];
export type ipamservicev1_LocationTreeNode = NonNullable<components['schemas']['GetLocationTreeResponse']['nodes']>[number];
