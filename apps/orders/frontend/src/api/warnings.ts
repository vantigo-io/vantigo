import { queryOptions } from "@tanstack/react-query";
import { json, ORDERS_QUERY_KEY, queryString, request, send } from "./request";
import type { Warning, WarningInput, WarningScope } from "./types";

const base = "/api/v1/orders/warnings";
const pathOf = (scope: WarningScope, id: number) => `${base}/${scope === "customer" ? "customers" : "products"}/${id}`;

/** Every sales warning (O33), customers' first, each by name; `scope` narrows to one kind. */
export const warningsQueryOptions = (scope?: WarningScope) =>
  queryOptions({
    queryKey: [ORDERS_QUERY_KEY, "warnings", "list", scope ?? "all"],
    queryFn: ({ signal }) => request<{ items: Warning[] }>(`${base}${queryString({ scope })}`, { signal }),
  });

/** Sets a customer's or a product's warning, replacing any it had (`orders:settings`). */
export const putWarning = (scope: WarningScope, id: number, input: WarningInput) =>
  send<Warning>(pathOf(scope, id), json("PUT", input));

/** Removes a customer's or a product's warning (`orders:settings`). */
export const deleteWarning = (scope: WarningScope, id: number) => send<void>(pathOf(scope, id), { method: "DELETE" });

/**
 * What a send, a confirm or an accept into a confirmed order adds to override
 * the document's block warnings (O33): only an `orders:settings` holder may,
 * with a reason.
 */
export interface WarningOverride {
  overrideWarnings: true;
  overrideReason: string;
}

export const overrideOf = (reason: string | null | undefined): WarningOverride | Record<string, never> =>
  reason ? { overrideWarnings: true, overrideReason: reason } : {};
