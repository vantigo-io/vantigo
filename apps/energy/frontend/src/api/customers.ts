import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export interface CustomerSummary {
  id: number;
  name: string;
  identity?: { name: string; id: string } | null;
}

interface CustomerListResponse {
  data: CustomerSummary[];
  pagination: { page: number; pageSize: number; totalCount: number; totalPages: number };
}

export const customersQueryOptions = (search = "") =>
  queryOptions({
    queryKey: ["energy", "customers", search],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ page: "1", pageSize: "50" });
      if (search.trim()) query.set("search", search.trim());
      return request<CustomerListResponse>(`/api/v1/customers?${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });
