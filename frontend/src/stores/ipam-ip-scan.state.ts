import { defineStore } from 'pinia';

import {
  IpScanService,
  type IpScanJob,
  type IpScanJobStatus,
  type ScanConfig,
  type GetScanJobResponse,
  type CancelScanResponse,
} from '../api/services';

type Paging = { page?: number; pageSize?: number } | undefined;

export const useIpamIpScanStore = defineStore('ipam-ip-scan', () => {
  /**
   * Start a new scan for a subnet
   */
  async function startScan(
    tenantId: number,
    subnetId: string,
    scanConfig?: ScanConfig,
    enableSnmp?: boolean,
    enableDnsUpdate?: boolean,
  ): Promise<GetScanJobResponse> {
    return await IpScanService.start({
      tenantId,
      subnetId,
      scanConfig,
      enableSnmp,
      enableDnsUpdate,
    });
  }

  /**
   * Get a scan job by ID
   */
  async function getScanJob(id: string): Promise<GetScanJobResponse> {
    return await IpScanService.get(id);
  }

  /**
   * List scan jobs
   */
  async function listScanJobs(
    paging?: Paging,
    formValues?: {
      subnetId?: string;
      status?: IpScanJobStatus;
    } | null,
  ): Promise<{ items: IpScanJob[]; total: number }> {
    const resp = await IpScanService.list({
      subnetId: formValues?.subnetId,
      status: formValues?.status,
      page: paging?.page,
      pageSize: paging?.pageSize,
    });
    return {
      items: resp.items ?? [],
      total: resp.total ?? 0,
    };
  }

  /**
   * Cancel a running or pending scan
   */
  async function cancelScan(id: string): Promise<CancelScanResponse> {
    return await IpScanService.cancel(id);
  }

  function $reset() {}

  return {
    $reset,
    startScan,
    getScanJob,
    listScanJobs,
    cancelScan,
  };
});
