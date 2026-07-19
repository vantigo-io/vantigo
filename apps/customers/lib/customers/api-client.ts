import type { Customer } from "@vantigo/customers/database/schema/customers";
import type { CustomerListResult, CustomerStats, DuplicateWarning } from "./queries";
import type { CustomerCreateInput, CustomerListQuery, CustomerUpdateInput } from "./schemas";

export type { Customer, CustomerListResult, CustomerStats, DuplicateWarning };

export interface CustomerWriteResult {
  customer: Customer;
  warnings: DuplicateWarning[];
}

export class ApiClientError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly details?: unknown,
  ) {
    super(message);
    this.name = "ApiClientError";
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
  });

  const body = await response.json().catch(() => null);
  if (!response.ok) {
    const error = (body as { error?: { code?: string; message?: string; details?: unknown } })
      ?.error;
    throw new ApiClientError(
      response.status,
      error?.code ?? "UNKNOWN",
      error?.message ?? `Request failed with status ${response.status}`,
      error?.details,
    );
  }
  return body as T;
}

export function fetchCustomers(query: Partial<CustomerListQuery>): Promise<CustomerListResult> {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  return request(`/api/customers?${params}`);
}

export async function fetchCustomer(id: number): Promise<Customer> {
  const { customer } = await request<{ customer: Customer }>(`/api/customers/${id}`);
  return customer;
}

export async function fetchCustomerStats(): Promise<CustomerStats> {
  const { stats } = await request<{ stats: CustomerStats }>("/api/customers/stats");
  return stats;
}

export function createCustomer(input: CustomerCreateInput): Promise<CustomerWriteResult> {
  return request("/api/customers", { method: "POST", body: JSON.stringify(input) });
}

export function updateCustomer(
  id: number,
  input: CustomerUpdateInput,
): Promise<CustomerWriteResult> {
  return request(`/api/customers/${id}`, { method: "PATCH", body: JSON.stringify(input) });
}

export async function archiveCustomer(id: number): Promise<Customer> {
  const { customer } = await request<{ customer: Customer }>(`/api/customers/${id}`, {
    method: "DELETE",
  });
  return customer;
}
