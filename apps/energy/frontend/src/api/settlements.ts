import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

/**
 * Settlements (energy consumption billing design D5, D11): what the supply
 * period's customer owes for a metering point over one billing period, as
 * `GET /api/v1/energy/metering-points/{id}/settlements` answers — needs
 * `energy:supply-periods-view` and `energy:consumption-view`.
 */

export type SettlementBlocker =
  | "period_open"
  | "no_tariff"
  | "no_consumption"
  | "incomplete"
  | "price_gap"
  | "price_preliminary";
export type SettlementState = "draft" | "invoiced" | "released";
export type SettlementKind = "regular" | "correction";

export type Settlement = components["schemas"]["SettlementResponse"];
export type SettlementLine = components["schemas"]["SettlementLine"];
export type SettlementInvoice = components["schemas"]["SettlementInvoice"];

export const settlementsQueryOptions = (meteringPointId: number) =>
  queryOptions({
    queryKey: ["energy", "metering-points", meteringPointId, "settlements"],
    queryFn: ({ signal }) =>
      request<Settlement[]>(`/api/v1/energy/metering-points/${meteringPointId}/settlements`, { signal }),
  });
