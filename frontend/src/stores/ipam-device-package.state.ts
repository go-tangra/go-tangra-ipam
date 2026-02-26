import { defineStore } from 'pinia';

import {
  DevicePackageService,
  type ListDevicePackagesResponse,
  type GetDevicePackageStatsResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamDevicePackageStore = defineStore('ipam-device-package', () => {
  /**
   * List packages for a device
   */
  async function listDevicePackages(
    deviceId: string,
    paging?: Paging,
    formValues?: {
      query?: string;
      needsUpdate?: boolean;
      isSecurityUpdate?: boolean;
      packageManager?: string;
    } | null,
    orderBy?: string[],
  ): Promise<ListDevicePackagesResponse> {
    return await DevicePackageService.list(deviceId, {
      query: formValues?.query,
      needsUpdate: formValues?.needsUpdate,
      isSecurityUpdate: formValues?.isSecurityUpdate,
      packageManager: formValues?.packageManager,
      page: paging?.page,
      pageSize: paging?.pageSize,
      orderBy,
    });
  }

  /**
   * Get package statistics for a device
   */
  async function getDevicePackageStats(
    deviceId: string,
  ): Promise<GetDevicePackageStatsResponse> {
    return await DevicePackageService.getStats(deviceId);
  }

  /**
   * Delete all packages for a device
   */
  async function deleteDevicePackages(deviceId: string): Promise<void> {
    return await DevicePackageService.delete(deviceId);
  }

  function $reset() {}

  return {
    $reset,
    listDevicePackages,
    getDevicePackageStats,
    deleteDevicePackages,
  };
});
