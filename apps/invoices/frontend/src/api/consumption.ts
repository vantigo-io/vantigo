import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { InvoiceDocument } from "./invoices";
import { INVOICES_QUERY_KEY, json, request } from "./request";

type Schemas = components["schemas"];

/**
 * The consumption views (energy consumption billing design D7, D11): a
 * customer's supplies with billing periods due, and the settlements the wizard
 * may take, as Energy answers them through this module.
 */
export type ConsumptionView = Schemas["InvoicesConsumptionResponse"];
export type DueSupply = Schemas["InvoicesDueSupply"];
export type SettlementsView = Schemas["InvoicesConsumptionSettlementsResponse"];
export type ConsumptionSettlement = Schemas["InvoicesConsumptionSettlement"];
export type SettlementLine = Schemas["InvoicesConsumptionSettlementLine"];
export type FromConsumptionInput = Schemas["InvoicesFromConsumptionRequest"];

/** The kind a line source names an energy settlement by on the wire. */
export const SETTLEMENT_KIND = "energy.settlement";

const query = (customerId: number, through?: string) =>
  `customerId=${customerId}${through ? `&through=${through}` : ""}`;

/** GET /invoices/consumption: the customer's supplies with periods due through the date (today when absent). */
export const getConsumption = (customerId: number, through?: string, signal?: AbortSignal): Promise<ConsumptionView> =>
  request<ConsumptionView>(`/api/v1/invoices/consumption?${query(customerId, through)}`, { signal });

export const consumptionQueryOptions = (customerId: number, through?: string) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "consumption", customerId, through ?? null],
    queryFn: ({ signal }) => getConsumption(customerId, through, signal),
  });

/**
 * GET /invoices/consumption/settlements: Energy settles the customer through
 * the date — writing its own draft settlements — and the wizard lists them.
 */
export const getConsumptionSettlements = (
  customerId: number,
  through?: string,
  signal?: AbortSignal,
): Promise<SettlementsView> =>
  request<SettlementsView>(`/api/v1/invoices/consumption/settlements?${query(customerId, through)}`, { signal });

export const consumptionSettlementsQueryOptions = (customerId: number, through?: string) =>
  queryOptions({
    queryKey: [INVOICES_QUERY_KEY, "consumption", "settlements", customerId, through ?? null],
    queryFn: ({ signal }) => getConsumptionSettlements(customerId, through, signal),
  });

/**
 * The wizard (D7): a new invoice draft of the named settlements — 201 — or,
 * with `invoiceId` and `revision`, their lines added to that draft — 200.
 * Either way the answer is the draft. The caller invalidates every invoices
 * query (`[INVOICES_QUERY_KEY]`).
 */
export const postFromConsumption = (input: FromConsumptionInput): Promise<InvoiceDocument> =>
  request<InvoiceDocument>("/api/v1/invoices/from-consumption", json("POST", input));
