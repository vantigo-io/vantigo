import { queryOptions } from "@tanstack/react-query";
import { type CustomerResponse, normalizeCustomer } from "./customers";
import { request } from "./request";

/**
 * A brand a customer or a group has chosen as its default (brands design B28).
 * `published` is offered and used; `archived` is still named but skipped, so the
 * default falls back to the next in the order; `unknown` is an id no published
 * brand has, and then `name` is absent (the wire omits a null name).
 */
export interface BrandChoice {
  id: string;
  name?: string | null;
  state: "published" | "archived" | "unknown";
}

/** Where a customer's effective brand comes from: its own choice, its group's, or the workspace default. */
export type BrandSource = "customer" | "group" | "default";

/**
 * A customer's default brand, resolved by the server on every read: the
 * customer's own brand, else its group's, else the workspace default. `own` and
 * `group` are the choices as made; `unavailable` names the ones that were
 * skipped because their brand is archived or unknown.
 */
export interface CustomerBrandInfo {
  own: BrandChoice | null;
  group: BrandChoice | null;
  effective: { id: string; name?: string | null; source: BrandSource };
  unavailable: ("customer" | "group")[];
}

/** A published brand the picker offers; the workspace default comes first. */
export interface BrandOption {
  id: string;
  name: string;
  isDefault: boolean;
}

/**
 * The brands a customer or a group may be given. One key under `["customers"]`,
 * so every broad invalidation refreshes it; the answer is a short list that
 * changes when someone publishes or archives a brand, so half a minute of
 * staleness is not visible.
 */
export const brandOptionsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "brands"],
    queryFn: ({ signal }) => request<BrandOption[]>("/api/v1/customers/brands", { signal }),
    staleTime: 30_000,
  });

/**
 * Sets the customer's own default brand, or clears it with `null` (the customer
 * then takes its group's, then the workspace default). It answers the whole
 * customer, so a caller reads the fresh revision straight off the response —
 * `setCustomerGroup`'s shape.
 */
export const setCustomerBrand = async (
  id: number,
  brandId: string | null,
  revision?: number,
): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request(`/api/v1/customers/${id}/brand`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ brandId, revision }),
    }),
  );
