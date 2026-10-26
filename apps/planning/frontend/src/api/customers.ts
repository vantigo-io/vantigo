import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * A cross-module read for the non-compete's customers (S14). Planning never
 * imports the customers frontend; it calls the customers HTTP API and
 * declares here the little of the shape it uses, as Projects does. The list
 * needs `customers:view`: a planner without it is answered 403, and the
 * picker then offers no search (the terms' customers stay named by the
 * server).
 */
export interface PlanningCustomerOption {
  id: number;
  name: string;
}

interface CustomerListResponse {
  data: PlanningCustomerOption[];
}

const CUSTOMER_SEARCH_PAGE_SIZE = 20;

/**
 * The customer picker's options. An empty term lists the first page. The key
 * is outside `PLANNING_QUERY_KEY`, so a Planning write's blanket invalidation
 * does not search another module's directory again; and the search runs only
 * once `enabled` — the picker opened or a term typed.
 */
export const customerSearchQueryOptions = (search: string, enabled = true) => {
  const term = search.trim();
  return queryOptions({
    queryKey: ["planning-customers-lookup", term],
    enabled,
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({ page: "1", pageSize: String(CUSTOMER_SEARCH_PAGE_SIZE) });
      if (term) query.set("search", term);
      const page = await request<CustomerListResponse>(`/api/v1/customers?${query}`, { signal });
      return page.data.map(({ id, name }) => ({ id, name }));
    },
    placeholderData: keepPreviousData,
    retry: false,
  });
};
