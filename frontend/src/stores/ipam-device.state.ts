import { defineStore } from 'pinia';

import {
  DeviceService,
  type Device,
  type DeviceStatus,
  type ListDevicesResponse,
  type GetDeviceResponse,
  type CreateDeviceResponse,
  type UpdateDeviceResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

// Device type enum
type DeviceType = 'DEVICE_TYPE_UNSPECIFIED' | 'DEVICE_TYPE_SERVER' | 'DEVICE_TYPE_SWITCH' | 'DEVICE_TYPE_ROUTER' | 'DEVICE_TYPE_FIREWALL' | 'DEVICE_TYPE_LOAD_BALANCER' | 'DEVICE_TYPE_STORAGE' | 'DEVICE_TYPE_VM' | 'DEVICE_TYPE_CONTAINER' | 'DEVICE_TYPE_OTHER';

export const useIpamDeviceStore = defineStore('ipam-device', () => {
  /**
   * List devices
   */
  async function listDevices(
    paging?: Paging,
    formValues?: {
      locationId?: string;
      deviceType?: DeviceType;
      status?: DeviceStatus;
      query?: string;
    } | null,
    orderBy?: string[],
  ): Promise<ListDevicesResponse> {
    return await DeviceService.list({
      locationId: formValues?.locationId,
      deviceType: formValues?.deviceType,
      status: formValues?.status,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
      orderBy,
    });
  }

  /**
   * Get a device by ID
   */
  async function getDevice(id: string): Promise<GetDeviceResponse> {
    return await DeviceService.get(id);
  }

  /**
   * Create a new device
   */
  async function createDevice(
    tenantId: number,
    data: Partial<Device>,
  ): Promise<CreateDeviceResponse> {
    return await DeviceService.create({
      tenantId,
      name: data.name!,
      deviceType: data.deviceType,
      description: data.description,
      manufacturer: data.manufacturer,
      model: data.model,
      serialNumber: data.serialNumber,
      assetTag: data.assetTag,
      locationId: data.locationId,
      rackId: data.rackId,
      rackPosition: data.rackPosition,
      deviceHeightU: data.deviceHeightU,
      status: data.status,
      primaryIp: data.primaryIp,
      managementIp: data.managementIp,
      osType: data.osType,
      osVersion: data.osVersion,
    });
  }

  /**
   * Update a device
   */
  async function updateDevice(
    id: string,
    data: Partial<Device>,
    updateMask: string[],
  ): Promise<UpdateDeviceResponse> {
    return await DeviceService.update(id, {
      id,
      data: data as Device,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete a device
   */
  async function deleteDevice(id: string): Promise<void> {
    return await DeviceService.delete(id);
  }

  function $reset() {}

  return {
    $reset,
    listDevices,
    getDevice,
    createDevice,
    updateDevice,
    deleteDevice,
  };
});
