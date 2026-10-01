// The state of one server-paged table inside a store: the visible page, the
// total, the filter and page request it was loaded with, and a reload that
// repeats that request (after a write or a live event). Rows are never
// prepended or appended locally: the server's order decides where a record
// lands.
import { ref, type Ref, type UnwrapRef } from 'vue'
import type { ListQueryOptions } from '@go-tangra/ui'
import { fetchPage, PAGE_SIZE, type ListParams, type Page } from '@/api/list'

/** The table options of a list (sortable fields as the server's Spec). */
export function listOptions(sortable: readonly string[], key: string, dir: 'asc' | 'desc'): ListQueryOptions {
  return { sortable: [...sortable], defaultSort: { key, dir }, defaultSize: PAGE_SIZE }
}

/** The first page in the list's default order. */
export function firstPage(o: ListQueryOptions): ListParams {
  return { page: 1, page_size: o.defaultSize ?? PAGE_SIZE, sort: o.defaultSort.key, order: o.defaultSort.dir }
}

type Filter = Record<string, string | number | boolean | undefined>

export interface PagedList<T, F extends object> {
  // Unwrapped like any ref'd state, so rows satisfy the table's row type.
  items: Ref<UnwrapRef<T[]>>
  total: Ref<number>
  params: Ref<ListParams>
  filter: Ref<F>
  loading: Ref<boolean>
  error: Ref<string>
  /** True once a page was requested (live events reload only then). */
  listed: Ref<boolean>
  /**
   * Loads one page with the filter (blank filter values are not sent).
   * Resolves with the page, or null when it failed or a newer request
   * superseded it (its rows are then ignored).
   */
  list(f?: F, q?: ListParams): Promise<Page<T> | null>
  /** Reloads the current page with the current filter and order. */
  reload(): Promise<Page<T> | null>
}

export function pagedList<T, F extends object>(path: string, opts: ListQueryOptions): PagedList<T, F> {
  const items = ref<T[]>([])
  const total = ref(0)
  const params = ref<ListParams>(firstPage(opts))
  const filter = ref({}) as Ref<F>
  const loading = ref(false)
  const error = ref('')
  const listed = ref(false)
  let seq = 0

  async function list(f: F = filter.value, q: ListParams = params.value): Promise<Page<T> | null> {
    const mine = ++seq
    filter.value = { ...f }
    params.value = { ...q }
    listed.value = true
    loading.value = true
    error.value = ''
    try {
      const res = await fetchPage<T>(path, { ...(f as Filter), ...q })
      if (mine !== seq) return null
      items.value = res.items as UnwrapRef<T[]>
      total.value = res.total
      return res
    } catch (e) {
      if (mine === seq) error.value = (e as Error).message
      return null
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  return { items, total, params, filter, loading, error, listed, list, reload: () => list() }
}
