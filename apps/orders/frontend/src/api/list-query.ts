/** The filters both lists take (O31a): customer, status, salesperson, a search and the page. */
export interface ListFilters {
  customerId?: number;
  status?: readonly string[];
  salespersonId?: string;
  q?: string;
  page?: number;
  pageSize?: number;
  /** Orders only: the ones not done and not cancelled. */
  open?: boolean;
}

/** The list's query string; a status filter is repeated, one parameter per status. */
export const listQuery = (filters: ListFilters): string => {
  const search = new URLSearchParams();
  if (filters.customerId !== undefined) search.set("customerId", String(filters.customerId));
  for (const status of filters.status ?? []) search.append("status", status);
  if (filters.salespersonId) search.set("salespersonId", filters.salespersonId);
  if (filters.q?.trim()) search.set("q", filters.q.trim());
  if (filters.open !== undefined) search.set("open", String(filters.open));
  if (filters.page !== undefined && filters.page > 1) search.set("page", String(filters.page));
  if (filters.pageSize !== undefined) search.set("pageSize", String(filters.pageSize));
  const text = search.toString();
  return text ? `?${text}` : "";
};

/** The page size both lists ask for. */
export const PAGE_SIZE = 25;
