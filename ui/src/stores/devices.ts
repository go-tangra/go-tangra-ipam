import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type {
  Device,
  DeviceInterface,
  DevicePackage,
  IPAddress,
  KvmSession,
  PowerAction,
  PowerStatus,
  SelEntry,
  Sensors,
} from '@/api/types'

export interface DeviceFilter {
  device_type?: string | undefined
  status?: string | undefined
  location_id?: string | undefined
  manufacturer?: string | undefined
  rack_id?: string | undefined
  q?: string | undefined
  cursor?: string | undefined
  limit?: number | undefined
}

export const useDevices = defineStore('ipam-devices', () => {
  const items = ref<Device[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(filter: DeviceFilter = {}): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: Device[] }>('GET', 'devices', undefined, { query: { ...filter } })
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function get(id: string): Promise<Device> {
    return api<Device>('GET', 'devices/' + id)
  }

  async function create(body: Partial<Device>): Promise<Device> {
    const d = await api<Device>('POST', 'devices', body)
    items.value = [d, ...items.value]
    return d
  }

  async function update(id: string, body: Partial<Device>): Promise<Device> {
    const d = await api<Device>('PUT', 'devices/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? d : x))
    return d
  }

  async function remove(id: string, force = false): Promise<void> {
    await api('DELETE', 'devices/' + id, undefined, { query: { force } })
    items.value = items.value.filter((x) => x.id !== id)
  }

  // --- related collections ---

  async function interfaces(id: string): Promise<DeviceInterface[]> {
    const res = await api<{ items: DeviceInterface[] }>('GET', 'devices/' + id + '/interfaces')
    return res.items ?? []
  }

  async function addInterface(id: string, body: Partial<DeviceInterface>): Promise<DeviceInterface> {
    return api<DeviceInterface>('POST', 'devices/' + id + '/interfaces', body)
  }

  async function removeInterface(id: string, ifid: string): Promise<void> {
    await api('DELETE', 'devices/' + id + '/interfaces/' + ifid)
  }

  async function packages(id: string): Promise<DevicePackage[]> {
    const res = await api<{ items: DevicePackage[] }>('GET', 'devices/' + id + '/packages')
    return res.items ?? []
  }

  async function syncPackages(id: string): Promise<DevicePackage[]> {
    const res = await api<{ items: DevicePackage[] }>('POST', 'devices/' + id + '/packages/sync', {})
    return res.items ?? []
  }

  async function addresses(id: string): Promise<IPAddress[]> {
    const res = await api<{ items: IPAddress[] }>('GET', 'devices/' + id + '/addresses')
    return res.items ?? []
  }

  // --- out-of-band (platform-admin) ---

  async function power(id: string): Promise<PowerStatus> {
    return api<PowerStatus>('GET', 'devices/' + id + '/power')
  }

  async function setPower(id: string, action: PowerAction): Promise<PowerStatus> {
    return api<PowerStatus>('POST', 'devices/' + id + '/power', { action })
  }

  async function sensors(id: string): Promise<Sensors> {
    return api<Sensors>('GET', 'devices/' + id + '/sensors')
  }

  async function sel(id: string): Promise<SelEntry[]> {
    const res = await api<{ items: SelEntry[] }>('GET', 'devices/' + id + '/sel')
    return res.items ?? []
  }

  async function kvmSession(id: string): Promise<KvmSession> {
    return api<KvmSession>('POST', 'devices/' + id + '/kvm-session', {})
  }

  return {
    items, loading, error,
    list, get, create, update, remove,
    interfaces, addInterface, removeInterface,
    packages, syncPackages, addresses,
    power, setPower, sensors, sel, kvmSession,
  }
})
