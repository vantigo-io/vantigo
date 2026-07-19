"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "./api-client";
import type {
  ContactCreateInput,
  ContactListQuery,
  ContactUpdateInput,
  CustomerContactCreateInput,
  CustomerContactUpdateInput,
} from "./schemas";

export const contactKeys = {
  all: ["contacts"] as const,
  lists: () => [...contactKeys.all, "list"] as const,
  list: (query: Partial<ContactListQuery>) => [...contactKeys.lists(), query] as const,
  details: () => [...contactKeys.all, "detail"] as const,
  detail: (id: number) => [...contactKeys.details(), id] as const,
  forCustomer: (customerId: number) => [...contactKeys.all, "customer", customerId] as const,
};

export function useContacts(query: Partial<ContactListQuery>, options?: { enabled?: boolean }) {
  return useQuery({
    queryKey: contactKeys.list(query),
    queryFn: () => api.fetchContacts(query),
    placeholderData: keepPreviousData,
    enabled: options?.enabled,
  });
}

export function useContact(id: number) {
  return useQuery({
    queryKey: contactKeys.detail(id),
    queryFn: () => api.fetchContact(id),
  });
}

export function useCustomerContacts(customerId: number) {
  return useQuery({
    queryKey: contactKeys.forCustomer(customerId),
    queryFn: () => api.fetchCustomerContacts(customerId),
  });
}

function useInvalidateContacts() {
  const queryClient = useQueryClient();
  return () => queryClient.invalidateQueries({ queryKey: contactKeys.all });
}

export function useCreateContact() {
  const invalidate = useInvalidateContacts();
  return useMutation({
    mutationFn: (input: ContactCreateInput) => api.createContact(input),
    onSuccess: invalidate,
  });
}

export function useUpdateContact() {
  const invalidate = useInvalidateContacts();
  return useMutation({
    mutationFn: ({ id, input }: { id: number; input: ContactUpdateInput }) =>
      api.updateContact(id, input),
    onSuccess: invalidate,
  });
}

export function useDeleteContact() {
  const invalidate = useInvalidateContacts();
  return useMutation({
    mutationFn: (id: number) => api.deleteContact(id),
    onSuccess: invalidate,
  });
}

export function useAddCustomerContact(customerId: number) {
  const invalidate = useInvalidateContacts();
  return useMutation({
    mutationFn: (input: CustomerContactCreateInput) => api.addCustomerContact(customerId, input),
    onSuccess: invalidate,
  });
}

export function useUpdateCustomerContact(customerId: number) {
  const invalidate = useInvalidateContacts();
  return useMutation({
    mutationFn: ({ contactId, input }: { contactId: number; input: CustomerContactUpdateInput }) =>
      api.updateCustomerContact(customerId, contactId, input),
    onSuccess: invalidate,
  });
}

export function useUnlinkCustomerContact(customerId: number) {
  const invalidate = useInvalidateContacts();
  return useMutation({
    mutationFn: (contactId: number) => api.unlinkCustomerContact(customerId, contactId),
    onSuccess: invalidate,
  });
}
