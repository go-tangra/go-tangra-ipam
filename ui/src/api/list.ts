// The list contract of the IPAM browser API (go-tangra
// specs/032-server-side-tables, contracts/http-list.md): every table endpoint
// takes page, page_size, sort and order and answers one page with the total.
import type { ListParams } from '@go-tangra/ui'
import { api } from './client'

export type { ListParams }

/** One page of a list endpoint. */
export interface Page<T> {
  items: T[]
  total: number
  /** The page returned: a page beyond the end answers the last page. */
  page?: number | undefined
  page_size?: number | undefined
  sort?: string | undefined
  order?: 'asc' | 'desc' | undefined
}

/** The largest page the server answers. */
export const MAX_PAGE_SIZE = 200
export const PAGE_SIZE = 25

type Query = Record<string, string | number | boolean | undefined>

/** Reads one page; a response without a total counts its own items. */
export async function fetchPage<T>(path: string, query: Query): Promise<Page<T>> {
  const res = await api<Partial<Page<T>>>('GET', path, undefined, { query })
  const items = res.items ?? []
  return { ...res, items, total: typeof res.total === 'number' ? res.total : items.length }
}

/**
 * Reads every record of a list by walking its pages at the largest size, for
 * pick lists and name lookups (not for tables: those page on the server).
 * The walk stops at the total, on a short page, or after `maxPages`.
 */
export async function fetchAll<T>(path: string, query: Query = {}, maxPages = 50): Promise<T[]> {
  const out: T[] = []
  for (let page = 1; page <= maxPages; page++) {
    const res = await fetchPage<T>(path, { ...query, page, page_size: MAX_PAGE_SIZE })
    out.push(...res.items)
    if (res.items.length < MAX_PAGE_SIZE || out.length >= res.total) break
  }
  return out
}
