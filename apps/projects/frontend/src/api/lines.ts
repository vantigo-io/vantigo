import { queryOptions } from "@tanstack/react-query";
import type { BillingLine, BillingLineInput } from "./projects";
import { request } from "./request";

export type { BillingLine, BillingLineInput, BillingLineListPrice, BillingLinePricing } from "./projects";

/**
 * Every line of the project, deactivated ones included, ordered by code. The API
 * answers 409 when the products module is off — the caller hides the section
 * instead, guided by the project's `billingLinesAvailable`.
 */
export const billingLinesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["projects", "detail", id, "billing-lines"],
    queryFn: ({ signal }) => request<BillingLine[]>(`/api/v1/projects/${id}/billing-lines`, { signal }),
  });

export const createBillingLine = (id: number, input: BillingLineInput): Promise<BillingLine> =>
  request<BillingLine>(`/api/v1/projects/${id}/billing-lines`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** Leaving `active` out of the input leaves the line as it stands; there is no delete. */
export const updateBillingLine = (id: number, lineId: number, input: BillingLineInput): Promise<BillingLine> =>
  request<BillingLine>(`/api/v1/projects/${id}/billing-lines/${lineId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
