import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * A cross-module read. Projects never imports the customers frontend; it calls the
 * customers HTTP API and declares here the little of the shape it uses, the way
 * `apps/energy/frontend/src/api/customers.ts` does.
 */
export interface CustomerOption {
  id: number;
  name: string;
}

interface CustomerListResponse {
  data: CustomerOption[];
}

const CUSTOMER_SEARCH_PAGE_SIZE = 20;

/** The customer picker's options. An empty term lists the first page. */
export const customerSearchQueryOptions = (search: string) => {
  const term = search.trim();
  return queryOptions({
    queryKey: ["projects", "customers", "search", term],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({ page: "1", pageSize: String(CUSTOMER_SEARCH_PAGE_SIZE) });
      if (term) query.set("search", term);
      const page = await request<CustomerListResponse>(`/api/v1/customers?${query}`, { signal });
      return page.data.map(({ id, name }) => ({ id, name }));
    },
    placeholderData: keepPreviousData,
  });
};

/** Names the customer a project bills to, for a page that only has the id. */
export const customerQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["projects", "customers", "detail", id],
    queryFn: ({ signal }) => request<CustomerOption>(`/api/v1/customers/${id}`, { signal }),
  });
