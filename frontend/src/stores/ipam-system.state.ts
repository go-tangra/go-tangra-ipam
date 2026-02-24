import { defineStore } from 'pinia';

import {
  SystemService,
  type HealthCheckResponse,
  type GetStatsResponse,
  type GetDnsConfigResponse,
  type UpdateDnsConfigResponse,
  type TestDnsConfigResponse,
  type DnsConfig,
} from '../api/services';

export const useIpamSystemStore = defineStore('ipam-system', () => {
  /**
   * Health check
   */
  async function healthCheck(): Promise<HealthCheckResponse> {
    return await SystemService.healthCheck();
  }

  /**
   * Get IPAM statistics
   */
  async function getStats(tenantId?: number): Promise<GetStatsResponse> {
    return await SystemService.getStats({ tenantId });
  }

  /**
   * Get DNS configuration
   */
  async function getDnsConfig(): Promise<GetDnsConfigResponse> {
    return await SystemService.getDnsConfig();
  }

  /**
   * Update DNS configuration
   */
  async function updateDnsConfig(
    config: Partial<DnsConfig>,
  ): Promise<UpdateDnsConfigResponse> {
    return await SystemService.updateDnsConfig(config);
  }

  /**
   * Test DNS configuration
   */
  async function testDnsConfig(): Promise<TestDnsConfigResponse> {
    return await SystemService.testDnsConfig();
  }

  function $reset() {}

  return {
    $reset,
    healthCheck,
    getStats,
    getDnsConfig,
    updateDnsConfig,
    testDnsConfig,
  };
});
