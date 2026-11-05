import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";
import type { Salesperson } from "./types";

/** The users who may sell — who hold orders:quotes-manage or orders:sales-manage — by display name. */
export const salespeopleQueryOptions = () =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "salespeople"],
    queryFn: ({ signal }) =>
      request<{ items: Salesperson[] }>("/api/v1/orders/salespeople", { signal }).then((a) => a.items),
    staleTime: 60_000,
  });
