import { queryOptions } from "@tanstack/react-query";
import { INVOICES_QUERY_KEY, request } from "./request";

/**
 * A customer as the buyer picker needs it: the customers module's own list,
 * read over HTTP because `contracts.CustomerDirectory` has no search (D1). It
 * needs `customers:view`, so the picker is only offered to a caller who holds
 * it; the invoices API itself takes a customer id and asks nothing more.
 *
 * The shape is typed here rather than generated: this package owns only the
 * invoices contract, and these four fields are all it reads of the answer.
 */
export interface CustomerOption {
  id: number;
  name: string;
  customerNumber: number;
  status: string;
}

interface CustomerListAnswer {
  data: CustomerOption[];
}

/**
 * A search of the customers. A buyer is picked among the active ones only —
 * the gates refuse the rest — while the list's filter offers every customer
 * not archived (`anyOpen`): a disabled customer still has documents to find.
 */
export const customerSearchQueryOptions = (search: string, anyOpen = false) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "customers", search, anyOpen],
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ pageSize: "20" });
      if (!anyOpen) params.set("status", "active");
      if (search.trim()) params.set("search", search.trim());
      const answer = await request<CustomerListAnswer>(`/api/v1/customers?${params.toString()}`, { signal });
      return answer.data;
    },
  });
