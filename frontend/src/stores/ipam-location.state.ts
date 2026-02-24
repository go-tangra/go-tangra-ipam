import { defineStore } from 'pinia';

import {
  LocationService,
  type Location,
  type LocationType,
  type LocationStatus,
  type ListLocationsResponse,
  type GetLocationResponse,
  type CreateLocationResponse,
  type UpdateLocationResponse,
  type GetLocationTreeResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamLocationStore = defineStore('ipam-location', () => {
  /**
   * List locations
   */
  async function listLocations(
    paging?: Paging,
    formValues?: {
      parentId?: string;
      locationType?: LocationType;
      status?: LocationStatus;
      query?: string;
    } | null,
  ): Promise<ListLocationsResponse> {
    return await LocationService.list({
      parentId: formValues?.parentId,
      locationType: formValues?.locationType,
      status: formValues?.status,
      query: formValues?.query,
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
  }

  /**
   * Get a location by ID
   */
  async function getLocation(id: string): Promise<GetLocationResponse> {
    return await LocationService.get(id);
  }

  /**
   * Create a new location
   */
  async function createLocation(
    tenantId: number,
    data: Partial<Location>,
  ): Promise<CreateLocationResponse> {
    return await LocationService.create({
      tenantId,
      name: data.name!,
      code: data.code,
      locationType: data.locationType,
      description: data.description,
      parentId: data.parentId,
      address: data.address,
      city: data.city,
      state: data.state,
      country: data.country,
      postalCode: data.postalCode,
    });
  }

  /**
   * Update a location
   */
  async function updateLocation(
    id: string,
    data: Partial<Location>,
    updateMask: string[],
  ): Promise<UpdateLocationResponse> {
    return await LocationService.update(id, {
      id,
      data: data as Location,
      updateMask: updateMask.join(','),
    });
  }

  /**
   * Delete a location
   */
  async function deleteLocation(id: string): Promise<void> {
    return await LocationService.delete(id);
  }

  /**
   * Get location tree structure
   */
  async function getLocationTree(
    rootId?: string,
    maxDepth?: number,
  ): Promise<GetLocationTreeResponse> {
    return await LocationService.getTree({ rootId, maxDepth });
  }

  function $reset() {}

  return {
    $reset,
    listLocations,
    getLocation,
    createLocation,
    updateLocation,
    deleteLocation,
    getLocationTree,
  };
});
