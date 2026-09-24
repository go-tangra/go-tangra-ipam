import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import type { HostGroup, HostGroupMember } from '@/api/types'

export const useHostGroups = defineStore('ipam-host-groups', () => {
  const items = ref<HostGroup[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: HostGroup[] }>('GET', 'host-groups')
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function get(id: string): Promise<HostGroup> {
    return api<HostGroup>('GET', 'host-groups/' + id)
  }

  async function create(body: Partial<HostGroup>): Promise<HostGroup> {
    const g = await api<HostGroup>('POST', 'host-groups', body)
    items.value = [g, ...items.value]
    return g
  }

  async function update(id: string, body: Partial<HostGroup>): Promise<HostGroup> {
    const g = await api<HostGroup>('PUT', 'host-groups/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? g : x))
    return g
  }

  async function remove(id: string): Promise<void> {
    await api('DELETE', 'host-groups/' + id)
    items.value = items.value.filter((x) => x.id !== id)
  }

  async function members(id: string): Promise<HostGroupMember[]> {
    const res = await api<{ items: HostGroupMember[] }>('GET', 'host-groups/' + id + '/members')
    return res.items ?? []
  }

  async function addMember(id: string, body: Partial<HostGroupMember>): Promise<HostGroupMember> {
    return api<HostGroupMember>('POST', 'host-groups/' + id + '/members', body)
  }

  async function updateMember(id: string, mid: string, body: Partial<HostGroupMember>): Promise<HostGroupMember> {
    return api<HostGroupMember>('PUT', 'host-groups/' + id + '/members/' + mid, body)
  }

  async function removeMember(id: string, mid: string): Promise<void> {
    await api('DELETE', 'host-groups/' + id + '/members/' + mid)
  }

  return { items, loading, error, list, get, create, update, remove, members, addMember, updateMember, removeMember }
})
