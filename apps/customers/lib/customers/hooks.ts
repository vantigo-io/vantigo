"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "./api-client";
import type { CustomerCreateInput, CustomerListQuery, CustomerUpdateInput } from "./schemas";

export const customerKeys = {
  all: ["customers"] as const,
  lists: () => [...customerKeys.all, "list"] as const,
  list: (query: Partial<CustomerListQuery>) => [...customerKeys.lists(), query] as const,
  details: () => [...customerKeys.all, "detail"] as const,
  detail: (id: number) => [...customerKeys.details(), id] as const,
  stats: () => [...customerKeys.all, "stats"] as const,
};

export function useCustomers(query: Partial<CustomerListQuery>) {
  return useQuery({
    queryKey: customerKeys.list(query),
    queryFn: () => api.fetchCustomers(query),
    placeholderData: keepPreviousData,
  });
}

export function useCustomer(id: number) {
  return useQuery({
    queryKey: customerKeys.detail(id),
    queryFn: () => api.fetchCustomer(id),
  });
}

export function useCustomerStats() {
  return useQuery({
    queryKey: customerKeys.stats(),
    queryFn: api.fetchCustomerStats,
  });
}

export function useCreateCustomer() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: CustomerCreateInput) => api.createCustomer(input),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: customerKeys.all });
    },
  });
}

export function useUpdateCustomer() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: number; input: CustomerUpdateInput }) =>
      api.updateCustomer(id, input),
    onSuccess: (result) => {
      queryClient.setQueryData(customerKeys.detail(result.customer.id), result.customer);
      queryClient.invalidateQueries({ queryKey: customerKeys.all });
    },
  });
}

export function useArchiveCustomer() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api.archiveCustomer(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: customerKeys.all });
    },
  });
}
