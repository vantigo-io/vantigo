import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";
import type { CustomerSummary } from "./types";

/** What the customer page's Orders tab shows above its lists, per currency (O31a). */
export const customerSummaryQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "customer-summary", customerId],
    queryFn: ({ signal }) => request<CustomerSummary>(`/api/v1/orders/customers/${customerId}/summary`, { signal }),
  });
