import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { type CustomerResponse, normalizeCustomer } from "./customers";
import { request } from "./request";

/**
 * A user who may be made a customer's owner (owner and tags design D1): the
 * directory's active users, at most twenty, narrowed by what the caller typed.
 * A disabled account is never offered — the API leaves it out — which is why
 * the picker keeps the CURRENT owner on its list separately: an owner disabled
 * after being assigned keeps the customer and must keep its name on screen.
 */
export interface AssignableUser {
  userId: string;
  displayName: string;
}

/**
 * `keepPreviousData` so the list does not blink empty between keystrokes, and
 * the trimmed term is both the query and the key, so " kari " and "kari" are
 * one cache entry rather than two requests for the same answer.
 *
 * The `staleTime` is why this key sitting under the `["customers"]` prefix costs
 * nothing: every save on the customer page invalidates that whole prefix, and
 * without it each one would send the directory search again underneath an open
 * picker. Half a minute of an answer that is a directory listing, not the
 * customer's own data, is not a staleness anyone can see.
 */
export const assignableUsersQueryOptions = (query: string) => {
  const term = query.trim();
  return queryOptions({
    queryKey: ["customers", "assignable-users", term],
    queryFn: ({ signal }) => {
      const search = term ? `?${new URLSearchParams({ query: term })}` : "";
      return request<AssignableUser[]>(`/api/v1/customers/assignable-users${search}`, { signal });
    },
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
};

/**
 * Sets or clears a customer's owner. It answers the whole customer, not just
 * the owner, so a caller can read the fresh revision straight off the response
 * — the same shape `updateContactInfo` has, and the reason `syncCustomerRevision`
 * can run before any invalidation.
 */
export const setCustomerOwner = async (
  id: number,
  ownerUserId: string | null,
  revision?: number,
): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request(`/api/v1/customers/${id}/owner`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ownerUserId, revision }),
    }),
  );
