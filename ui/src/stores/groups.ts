import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '@/api/client'
import { fetchPage, type ListParams, type Page } from '@/api/list'
import { listOptions } from './paged'

/** Sortable fields of a group's members (server Specs store.IPMemberList / HostMemberList). */
export const MEMBER_LIST = listOptions(['sequence', 'name'], 'sequence', 'asc')
import type { GroupMatch, IPGroup, IPGroupMember } from '@/api/types'

export const useGroups = defineStore('ipam-ip-groups', () => {
  const items = ref<IPGroup[]>([])
  const loading = ref(false)
  const error = ref('')

  async function list(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await api<{ items: IPGroup[] }>('GET', 'ip-groups')
      items.value = res.items ?? []
    } catch (e) {
      error.value = (e as Error).message
    } finally {
      loading.value = false
    }
  }

  async function get(id: string): Promise<IPGroup> {
    return api<IPGroup>('GET', 'ip-groups/' + id)
  }

  async function create(body: Partial<IPGroup>): Promise<IPGroup> {
    const g = await api<IPGroup>('POST', 'ip-groups', body)
    items.value = [g, ...items.value]
    return g
  }

  async function update(id: string, body: Partial<IPGroup>): Promise<IPGroup> {
    const g = await api<IPGroup>('PUT', 'ip-groups/' + id, body)
    items.value = items.value.map((x) => (x.id === id ? g : x))
    return g
  }

  async function remove(id: string): Promise<void> {
    await api('DELETE', 'ip-groups/' + id)
    items.value = items.value.filter((x) => x.id !== id)
  }

  // members reads one page of a group's members.
  async function members(id: string, q: ListParams): Promise<Page<IPGroupMember>> {
    return fetchPage<IPGroupMember>('ip-groups/' + id + '/members', { ...q })
  }

  async function addMember(id: string, body: Partial<IPGroupMember>): Promise<IPGroupMember> {
    return api<IPGroupMember>('POST', 'ip-groups/' + id + '/members', body)
  }

  async function updateMember(id: string, mid: string, body: Partial<IPGroupMember>): Promise<IPGroupMember> {
    return api<IPGroupMember>('PUT', 'ip-groups/' + id + '/members/' + mid, body)
  }

  async function removeMember(id: string, mid: string): Promise<void> {
    await api('DELETE', 'ip-groups/' + id + '/members/' + mid)
  }

  // checkIp returns the groups whose membership contains an address.
  async function checkIp(ip: string): Promise<GroupMatch[]> {
    const res = await api<{ matching_groups: IPGroup[] | null }>('GET', 'ip-groups/check', undefined, { query: { ip } })
    return (res.matching_groups ?? []).map((g) => ({ group_id: g.id, name: g.name }))
  }

  return { items, loading, error, list, get, create, update, remove, members, addMember, updateMember, removeMember, checkIp }
})
