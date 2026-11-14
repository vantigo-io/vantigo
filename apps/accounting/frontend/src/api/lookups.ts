import { queryOptions } from "@tanstack/react-query";
import { ACCOUNTING_QUERY_KEY, request } from "./request";

/**
 * What this package reads of other modules, over HTTP (module boundaries
 * rule 7): a customer search for a line's party and the active projects for
 * its project. The shapes are typed here rather than generated: this package
 * owns only the accounting contract, and these are the fields it reads. A
 * caller without the customers' or the projects' permission gets no picker.
 */
export interface CustomerOption {
  id: number;
  name: string;
  customerNumber: number;
}

export interface ProjectOption {
  id: number;
  code: string;
  name: string;
}

export const customerSearchQueryOptions = (search: string) =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "customers", search],
    queryFn: async ({ signal }) => {
      const params = new URLSearchParams({ pageSize: "20", includeArchived: "true" });
      if (search.trim()) params.set("search", search.trim());
      const answer = await request<{ data: CustomerOption[] }>(`/api/v1/customers?${params.toString()}`, { signal });
      return answer.data;
    },
    retry: false,
  });

export const projectsQueryOptions = () =>
  queryOptions({
    queryKey: [ACCOUNTING_QUERY_KEY, "projects"],
    queryFn: async ({ signal }) => {
      const answer = await request<{ data: ProjectOption[] }>("/api/v1/projects?status=active&pageSize=100", {
        signal,
      });
      return answer.data.map(({ id, code, name }) => ({ id, code, name }));
    },
    retry: false,
    staleTime: 60_000,
  });
