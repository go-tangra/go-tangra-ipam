import { defineStore } from 'pinia'
import { api } from '@/api/client'
import { fetchAll, fetchPage, type ListParams, type Page } from '@/api/list'
import { listOptions, pagedList } from './paged'
import type {
  BmcStatus,
  Device,
  DeviceHardware,
  DeviceInterface,
  DevicePackage,
  IPAddress,
  KvmSession,
  PowerAction,
  PowerStatus,
  SelEntry,
  Sensor,
} from '@/api/types'

/** Sortable fields of GET /devices (server Spec store.DeviceList). */
export const DEVICE_SORTS = ['name', 'device_type', 'status', 'manufacturer', 'location', 'created_at'] as const
export const DEVICE_LIST = listOptions(DEVICE_SORTS, 'name', 'asc')
// The device's own tables (server Specs store.InterfaceList, PackageList,
// AddressList).
export const INTERFACE_LIST = listOptions(['name'], 'name', 'asc')
export const PACKAGE_LIST = listOptions(['name', 'version'], 'name', 'asc')
export const DEVICE_ADDRESS_LIST = listOptions(['address', 'hostname', 'mac', 'status', 'address_type', 'last_seen', 'created_at'], 'address', 'asc')

export interface PackageFilter {
  needs_update?: boolean | undefined
  security_only?: boolean | undefined
  manager?: string | undefined
}

export interface DeviceFilter {
  device_type?: string | undefined
  status?: string | undefined
  location_id?: string | undefined
  manufacturer?: string | undefined
  rack_id?: string | undefined
  source?: string | undefined
  report_state?: string | undefined
  has_hardware?: 'true' | 'false' | undefined
  query?: string | undefined
}

export const useDevices = defineStore('ipam-devices', () => {
  // The table page (server order); writes reload it rather than insert rows.
  const { items, total, params, filter, loading, error, listed, list, reload } = pagedList<Device, DeviceFilter>('devices', DEVICE_LIST)
  const refresh = async () => (listed.value ? reload() : null)

  // lookup lists every matching device without replacing `items` (pick lists
  // and names for references such as the device that reported an ARP-learned
  // MAC).
  async function lookup(filter: DeviceFilter = {}): Promise<Device[]> {
    return fetchAll<Device>('devices', { ...filter })
  }

  // inRack lists the devices mounted in a rack without replacing `items`, so a
  // rack elevation can load alongside the device list.
  async function inRack(rackId: string): Promise<Device[]> {
    return fetchAll<Device>('devices', { rack_id: rackId })
  }

  async function get(id: string): Promise<Device> {
    return api<Device>('GET', 'devices/' + id)
  }

  async function create(body: Partial<Device>): Promise<Device> {
    const d = await api<Device>('POST', 'devices', body)
    void refresh()
    return d
  }

  async function update(id: string, body: Partial<Device>): Promise<Device> {
    // The hardware summary is server-owned (reported data): the API refuses it.
    const rest: Partial<Device> = { ...body }
    delete rest.hardware_summary
    const d = await api<Device>('PUT', 'devices/' + id, rest)
    items.value = items.value.map((x) => (x.id === id ? d : x))
    return d
  }

  async function remove(id: string, force = false): Promise<void> {
    await api('DELETE', 'devices/' + id, undefined, { query: { force } })
    items.value = items.value.filter((x) => x.id !== id)
    void refresh()
  }

  // --- related collections ---

  // One page of a device's interfaces / packages / addresses (its tables).
  async function interfaces(id: string, q: ListParams): Promise<Page<DeviceInterface>> {
    return fetchPage<DeviceInterface>('devices/' + id + '/interfaces', { ...q })
  }

  async function addInterface(id: string, body: Partial<DeviceInterface>): Promise<DeviceInterface> {
    return api<DeviceInterface>('POST', 'devices/' + id + '/interfaces', body)
  }

  async function removeInterface(id: string, ifid: string): Promise<void> {
    await api('DELETE', 'devices/' + id + '/interfaces/' + ifid)
  }

  async function packages(id: string, q: ListParams, f: PackageFilter = {}): Promise<Page<DevicePackage>> {
    return fetchPage<DevicePackage>('devices/' + id + '/packages', { ...f, ...q })
  }

  // hardware reads the device's reported hardware (feature 023).
  async function hardware(id: string): Promise<DeviceHardware> {
    return api<DeviceHardware>('GET', 'devices/' + id + '/hardware')
  }

  async function addresses(id: string, q: ListParams): Promise<Page<IPAddress>> {
    return fetchPage<IPAddress>('devices/' + id + '/addresses', { ...q })
  }

  // --- out-of-band (platform-admin) ---

  async function power(id: string): Promise<PowerStatus> {
    return api<PowerStatus>('GET', 'devices/' + id + '/power')
  }

  // setPower sends a chassis action; the BMC only acknowledges it, so callers
  // re-read power() for the resulting state.
  async function setPower(id: string, action: PowerAction): Promise<void> {
    await api<{ accepted: boolean }>('POST', 'devices/' + id + '/power', { action })
  }

  async function sensors(id: string): Promise<Sensor[]> {
    const res = await api<{ items: Sensor[] | null }>('GET', 'devices/' + id + '/sensors')
    return res.items ?? []
  }

  async function sel(id: string): Promise<SelEntry[]> {
    const res = await api<{ items: SelEntry[] }>('GET', 'devices/' + id + '/sel')
    return res.items ?? []
  }

  async function kvmSession(id: string): Promise<KvmSession> {
    return api<KvmSession>('POST', 'devices/' + id + '/kvm-session', {})
  }

  // --- BMC credentials (feature 024): a Warden secret reference ---

  async function bmc(id: string): Promise<BmcStatus> {
    return api<BmcStatus>('GET', 'devices/' + id + '/bmc')
  }

  async function setBmc(id: string, reference: string): Promise<BmcStatus> {
    return api<BmcStatus>('PUT', 'devices/' + id + '/bmc', { reference })
  }

  async function clearBmc(id: string): Promise<void> {
    await api('DELETE', 'devices/' + id + '/bmc')
  }

  return {
    items, total, params, filter, loading, error, listed,
    list, reload, lookup, inRack, get, create, update, remove,
    interfaces, addInterface, removeInterface,
    packages, addresses, hardware,
    power, setPower, sensors, sel, kvmSession,
    bmc, setBmc, clearBmc,
  }
})
