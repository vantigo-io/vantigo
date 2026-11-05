import { queryOptions } from "@tanstack/react-query";
import { ORDERS_QUERY_KEY, request } from "./request";

/** A project a document may name (O13): the customer's own, or an internal one. */
export interface ProjectOption {
  id: number;
  code?: string | null;
  name: string;
}

/** The customer's active projects (`projects:access`); the server checks the pick (`project_customer_mismatch`). */
export const customerProjectsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "projects", customerId],
    retry: false,
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ customerId: String(customerId), status: "active", pageSize: "100" });
      const answer = await request<{ data: ProjectOption[] }>(`/api/v1/projects?${params.toString()}`, { signal });
      return answer.data;
    },
  });
