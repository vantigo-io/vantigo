import { queryOptions } from "@tanstack/react-query";
import { type CustomerResponse, normalizeCustomer } from "./customers";
import { request } from "./request";

/**
 * A group in the installation's vocabulary (customer groups design D2).
 * `defaultPaymentTermsDays` is null when the group decides nothing — the wire
 * omits the field in that case, and absent and null mean the same thing, which
 * is decided here so nothing downstream has to know.
 */
export interface CustomerGroup {
  id: string;
  name: string;
  defaultPaymentTermsDays: number | null;
}

/** A group with how many customers belong to it — what the vocabulary list answers. */
export interface CustomerGroupSummary extends CustomerGroup {
  customerCount: number;
}

type RawCustomerGroupSummary = Omit<CustomerGroupSummary, "defaultPaymentTermsDays"> & {
  defaultPaymentTermsDays?: number | null;
};

const normalizeGroupSummary = (raw: RawCustomerGroupSummary): CustomerGroupSummary => ({
  id: raw.id,
  name: raw.name,
  defaultPaymentTermsDays: raw.defaultPaymentTermsDays ?? null,
  customerCount: raw.customerCount,
});

/**
 * Every group, name-ascending, with its member count. One key for the whole
 * installation — a vocabulary is not paginated and not per-customer — under the
 * `["customers"]` prefix, so every existing broad invalidation refreshes it too.
 */
export const customerGroupsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "groups"],
    queryFn: async ({ signal }) =>
      (await request<RawCustomerGroupSummary[]>("/api/v1/customers/groups", { signal })).map(normalizeGroupSummary),
  });

/**
 * Both writes send BOTH fields, because `PUT` is a full replace: an emptied
 * default clears the group's default rather than leaving the one it had (design
 * D2), and a caller that omitted the field would be asking for the opposite of
 * what the form shows.
 */
export interface GroupInput {
  name: string;
  defaultPaymentTermsDays: number | null;
}

export const createGroup = async (input: GroupInput): Promise<CustomerGroupSummary> =>
  normalizeGroupSummary(
    await request<RawCustomerGroupSummary>("/api/v1/customers/groups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const updateGroup = async (id: string, input: GroupInput): Promise<CustomerGroupSummary> =>
  normalizeGroupSummary(
    await request<RawCustomerGroupSummary>(`/api/v1/customers/groups/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const deleteGroup = (id: string) => request<void>(`/api/v1/customers/groups/${id}`, { method: "DELETE" });

/**
 * Puts a customer in a group, or takes it out of every group. It answers the
 * whole customer, not just the group, so a caller reads the fresh revision
 * straight off the response — the same shape `setCustomerOwner` has, and the
 * reason `syncCustomerRevision` can run before any invalidation.
 */
export const setCustomerGroup = async (
  id: number,
  groupId: string | null,
  revision?: number,
): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request(`/api/v1/customers/${id}/group`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ groupId, revision }),
    }),
  );
