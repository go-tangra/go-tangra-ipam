/**
 * CIDR utility functions for subnet calculations
 */

export interface ParsedCIDR {
  ip: number[];
  prefix: number;
  networkInt: number;
  broadcastInt: number;
  totalAddresses: number;
  usableHosts: number;
}

export interface SubnetSuggestion {
  cidr: string;
  networkAddress: string;
  broadcastAddress: string;
  usableHosts: number;
}

export interface SubnetDivision {
  prefix: number;
  count: number;
  hostsPerSubnet: number;
}

export interface SubnetMapSegment {
  type: 'used' | 'available' | 'suggested';
  cidr?: string;
  name?: string;
  startAddress: string;
  endAddress: string;
  percentage: number;
}

export interface VLSMRequirement {
  name: string;
  hostsNeeded: number;
}

export interface VLSMAllocation {
  name: string;
  cidr: string;
  hostsNeeded: number;
  usableHosts: number;
  wastedAddresses: number;
}

/**
 * Check if a CIDR string is IPv6
 */
export function isIPv6(cidr: string): boolean {
  return cidr.includes(':');
}

/**
 * Parse a CIDR string into components (IPv4 only)
 */
export function parseCIDR(cidr: string): ParsedCIDR | null {
  if (isIPv6(cidr)) {
    return null; // IPv6 not supported yet
  }

  const match = cidr.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)\/(\d+)$/);
  if (!match) return null;

  const ip = [
    parseInt(match[1]!, 10),
    parseInt(match[2]!, 10),
    parseInt(match[3]!, 10),
    parseInt(match[4]!, 10),
  ];

  // Validate IP octets
  if (ip.some((octet) => octet < 0 || octet > 255)) {
    return null;
  }

  const prefix = parseInt(match[5]!, 10);
  if (prefix < 0 || prefix > 32) return null;

  const networkInt = (ip[0]! << 24) | (ip[1]! << 16) | (ip[2]! << 8) | ip[3]!;
  const mask = prefix === 0 ? 0 : ~((1 << (32 - prefix)) - 1);
  const networkAddr = networkInt & mask;
  const broadcastInt = networkAddr | ~mask;

  const totalAddresses = Math.pow(2, 32 - prefix);
  const usableHosts = prefix >= 31 ? totalAddresses : totalAddresses - 2;

  return {
    ip,
    prefix,
    networkInt: networkAddr >>> 0, // Convert to unsigned
    broadcastInt: broadcastInt >>> 0,
    totalAddresses,
    usableHosts,
  };
}

/**
 * Convert an integer to IP address string
 */
export function intToIP(int: number): string {
  return [
    (int >>> 24) & 255,
    (int >>> 16) & 255,
    (int >>> 8) & 255,
    int & 255,
  ].join('.');
}

/**
 * Convert prefix length to number of usable hosts
 */
export function prefixToHosts(prefix: number): number {
  if (prefix >= 31) return Math.pow(2, 32 - prefix);
  return Math.pow(2, 32 - prefix) - 2;
}

/**
 * Convert prefix length to total addresses
 */
export function prefixToAddresses(prefix: number): number {
  return Math.pow(2, 32 - prefix);
}

/**
 * Calculate the minimum prefix needed for a given number of hosts
 */
export function hostsToMinPrefix(hosts: number): number {
  // Need to account for network and broadcast addresses
  const totalNeeded = hosts + 2;
  const bits = Math.ceil(Math.log2(totalNeeded));
  return 32 - bits;
}

/**
 * Format address count for display (e.g., "256" or "65K")
 */
export function formatAddressCount(count: number): string {
  if (count >= 1000000) {
    return `${(count / 1000000).toFixed(1)}M`;
  }
  if (count >= 1000) {
    return `${(count / 1000).toFixed(1)}K`;
  }
  return count.toString();
}

/**
 * Check if a child CIDR is valid within a parent CIDR
 */
export function isValidChildCidr(parentCidr: string, childCidr: string): boolean {
  const parent = parseCIDR(parentCidr);
  const child = parseCIDR(childCidr);

  if (!parent || !child) return false;

  // Child prefix must be larger (more specific) than parent
  if (child.prefix <= parent.prefix) return false;

  // Child network must be within parent range
  return (
    child.networkInt >= parent.networkInt &&
    child.broadcastInt <= parent.broadcastInt
  );
}

/**
 * Check if two CIDRs overlap
 */
export function cidrsOverlap(cidr1: string, cidr2: string): boolean {
  const parsed1 = parseCIDR(cidr1);
  const parsed2 = parseCIDR(cidr2);

  if (!parsed1 || !parsed2) return false;

  // Check if either network is within the other
  return !(
    parsed1.broadcastInt < parsed2.networkInt ||
    parsed2.broadcastInt < parsed1.networkInt
  );
}

/**
 * Get available subnets at a specific prefix within a parent,
 * excluding existing children
 */
export function getAvailableSubnets(
  parentCidr: string,
  existingChildren: string[],
  targetPrefix: number,
): SubnetSuggestion[] {
  const parent = parseCIDR(parentCidr);
  if (!parent || targetPrefix <= parent.prefix || targetPrefix > 30) {
    return [];
  }

  const subnetSize = Math.pow(2, 32 - targetPrefix);
  const suggestions: SubnetSuggestion[] = [];

  // Iterate through all possible subnets at the target prefix
  for (
    let netAddr = parent.networkInt;
    netAddr <= parent.broadcastInt;
    netAddr += subnetSize
  ) {
    const broadcastAddr = netAddr + subnetSize - 1;

    // Make sure we're still within the parent
    if (broadcastAddr > parent.broadcastInt) break;

    const cidr = `${intToIP(netAddr)}/${targetPrefix}`;

    // Check if this subnet overlaps with any existing children
    const overlaps = existingChildren.some((child) => cidrsOverlap(cidr, child));

    if (!overlaps) {
      suggestions.push({
        cidr,
        networkAddress: intToIP(netAddr),
        broadcastAddress: intToIP(broadcastAddr),
        usableHosts: prefixToHosts(targetPrefix),
      });
    }
  }

  return suggestions;
}

/**
 * Calculate possible subnet divisions for a parent
 */
export function calculateSubnetDivisions(parentCidr: string): SubnetDivision[] {
  const parent = parseCIDR(parentCidr);
  if (!parent) return [];

  const divisions: SubnetDivision[] = [];
  const maxDivisions = 6; // Limit to reasonable number

  for (let i = 1; i <= maxDivisions && parent.prefix + i <= 30; i++) {
    const newPrefix = parent.prefix + i;
    divisions.push({
      prefix: newPrefix,
      count: Math.pow(2, i),
      hostsPerSubnet: prefixToHosts(newPrefix),
    });
  }

  return divisions;
}

/**
 * VLSM optimization - allocate subnets based on host requirements
 * Uses a greedy approach, allocating largest requirements first
 */
export function vlsmOptimize(
  parentCidr: string,
  requirements: VLSMRequirement[],
  existingChildren: string[] = [],
): {
  allocations: VLSMAllocation[];
  unallocated: VLSMRequirement[];
  totalWasted: number;
} {
  const parent = parseCIDR(parentCidr);
  if (!parent) {
    return { allocations: [], unallocated: requirements, totalWasted: 0 };
  }

  // Sort requirements by hosts needed (descending)
  const sortedReqs = [...requirements].sort(
    (a, b) => b.hostsNeeded - a.hostsNeeded,
  );

  const allocations: VLSMAllocation[] = [];
  const unallocated: VLSMRequirement[] = [];
  let totalWasted = 0;

  // Track used ranges
  const usedRanges: Array<{ start: number; end: number }> = existingChildren
    .map((cidr) => {
      const parsed = parseCIDR(cidr);
      return parsed ? { start: parsed.networkInt, end: parsed.broadcastInt } : null;
    })
    .filter((r): r is { start: number; end: number } => r !== null);

  // Helper to check if a range is available
  function isRangeAvailable(start: number, end: number): boolean {
    return !usedRanges.some(
      (r) => !(end < r.start || start > r.end),
    );
  }

  // Helper to find next available block of given size
  function findAvailableBlock(
    size: number,
    prefix: number,
  ): { start: number; cidr: string } | null {
    // Blocks must be aligned to their size
    // Note: parent is guaranteed non-null here due to early return above
    const alignedStart =
      Math.ceil(parent!.networkInt / size) * size;

    for (
      let start = alignedStart;
      start + size - 1 <= parent!.broadcastInt;
      start += size
    ) {
      const end = start + size - 1;
      if (isRangeAvailable(start, end)) {
        return {
          start,
          cidr: `${intToIP(start)}/${prefix}`,
        };
      }
    }
    return null;
  }

  // Allocate each requirement
  for (const req of sortedReqs) {
    const prefix = hostsToMinPrefix(req.hostsNeeded);

    if (prefix < parent.prefix || prefix > 30) {
      unallocated.push(req);
      continue;
    }

    const size = Math.pow(2, 32 - prefix);
    const block = findAvailableBlock(size, prefix);

    if (block) {
      const usableHosts = prefixToHosts(prefix);
      const wasted = usableHosts - req.hostsNeeded;

      allocations.push({
        name: req.name,
        cidr: block.cidr,
        hostsNeeded: req.hostsNeeded,
        usableHosts,
        wastedAddresses: wasted,
      });

      totalWasted += wasted;

      // Mark range as used
      usedRanges.push({
        start: block.start,
        end: block.start + size - 1,
      });
    } else {
      unallocated.push(req);
    }
  }

  return { allocations, unallocated, totalWasted };
}

/**
 * Generate subnet map segments for visualization
 */
export function generateSubnetMap(
  parentCidr: string,
  existingChildren: Array<{ cidr: string; name?: string }>,
  suggestedCidrs: string[] = [],
): SubnetMapSegment[] {
  const parent = parseCIDR(parentCidr);
  if (!parent) return [];

  const segments: SubnetMapSegment[] = [];
  const parentSize = parent.totalAddresses;

  // Combine existing and suggested into sorted list
  interface ChildInfo {
    cidr: string;
    type: 'used' | 'suggested';
    name?: string;
    parsed: ParsedCIDR;
  }

  const children: ChildInfo[] = [];

  for (const child of existingChildren) {
    const parsed = parseCIDR(child.cidr);
    if (parsed) {
      children.push({ cidr: child.cidr, type: 'used', name: child.name, parsed });
    }
  }

  for (const cidr of suggestedCidrs) {
    const parsed = parseCIDR(cidr);
    if (parsed) {
      const overlapsWithExisting = existingChildren.some((c) => cidrsOverlap(cidr, c.cidr));
      if (!overlapsWithExisting) {
        children.push({ cidr, type: 'suggested', parsed });
      }
    }
  }

  // Sort by network address
  children.sort((a, b) => a.parsed.networkInt - b.parsed.networkInt);

  let currentPos = parent.networkInt;

  for (const child of children) {
    // Add available segment before this child if there's a gap
    if (child.parsed.networkInt > currentPos) {
      const gapSize = child.parsed.networkInt - currentPos;
      segments.push({
        type: 'available',
        startAddress: intToIP(currentPos),
        endAddress: intToIP(child.parsed.networkInt - 1),
        percentage: (gapSize / parentSize) * 100,
      });
    }

    // Add the child segment
    const childSize = child.parsed.totalAddresses;
    segments.push({
      type: child.type,
      cidr: child.cidr,
      name: child.name,
      startAddress: intToIP(child.parsed.networkInt),
      endAddress: intToIP(child.parsed.broadcastInt),
      percentage: (childSize / parentSize) * 100,
    });

    currentPos = child.parsed.broadcastInt + 1;
  }

  // Add remaining space at the end
  if (currentPos <= parent.broadcastInt) {
    const remainingSize = parent.broadcastInt - currentPos + 1;
    segments.push({
      type: 'available',
      startAddress: intToIP(currentPos),
      endAddress: intToIP(parent.broadcastInt),
      percentage: (remainingSize / parentSize) * 100,
    });
  }

  return segments;
}
