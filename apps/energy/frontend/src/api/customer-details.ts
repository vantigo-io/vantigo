import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export interface EnergyCustomer {
  id: number;
  name: string;
}

export const customerQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["energy", "customer", id],
    queryFn: ({ signal }) => request<EnergyCustomer>(`/api/v1/customers/${id}`, { signal }),
  });
