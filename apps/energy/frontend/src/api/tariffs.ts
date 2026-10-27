import { type QueryClient, queryOptions } from "@tanstack/react-query";
import type { BillingCadence, SupplyPeriod } from "./energy";
import { NotFoundError, request } from "./request";

/**
 * Tariffs (energy consumption billing design D3): a power tariff is the
 * supplier's contract, assigned to supply periods; a grid tariff is the
 * installation's own, assigned to metering points. Each is versioned by date,
 * a version holding its components in print order.
 */

export type TariffKind = "power" | "grid";
export type TariffStatus = "active" | "archived";
export type TariffComponentKind =
  | "spot_energy"
  | "fixed_energy"
  | "markup"
  | "monthly_fee"
  | "grid_energy"
  | "grid_capacity"
  | "grid_fixed"
  | "electricity_tax"
  | "enova_levy";

/** The kinds each tariff kind may hold, in the order the editor offers them. */
export const TARIFF_COMPONENT_KINDS: Record<TariffKind, readonly TariffComponentKind[]> = {
  power: ["spot_energy", "fixed_energy", "markup", "monthly_fee"],
  grid: ["grid_energy", "grid_capacity", "grid_fixed", "electricity_tax", "enova_levy", "monthly_fee"],
};

/** A capacity step: from `fromKw` (the month's three highest hours on three days, averaged), `perMonth` NOK a month. */
export interface TariffTier {
  fromKw: number;
  perMonth: number;
}

/** One priced element of a version; a field its kind does not use is null. */
export interface TariffComponent {
  id: number;
  position: number;
  kind: TariffComponentKind;
  name: string;
  rate: number | null;
  rateNight: number | null;
  dayFrom: number | null;
  dayTo: number | null;
  weekdaysOnly: boolean;
  tiers: TariffTier[] | null;
  rateReduced: number | null;
  rateBusiness: number | null;
}

/** A version applies on the civil days [validFrom, validTo); `validTo` null is open-ended. */
export interface TariffVersion {
  id: number;
  validFrom: string;
  validTo: string | null;
  components: TariffComponent[];
}

/** How many supply periods (of any status) and metering points name the tariff. */
export interface TariffUsage {
  supplyPeriods: number;
  meteringPoints: number;
}

export interface Tariff {
  id: number;
  kind: TariffKind;
  name: string;
  description: string | null;
  status: TariffStatus;
  createdAt: string;
  updatedAt: string;
  usage: TariffUsage;
  versions: TariffVersion[];
}

/** A tariff as the list shows it, with the version valid today (Europe/Oslo), null when none is. */
export interface TariffSummary {
  id: number;
  kind: TariffKind;
  name: string;
  description: string | null;
  status: TariffStatus;
  createdAt: string;
  updatedAt: string;
  versionCount: number;
  currentVersion: { validFrom: string; validTo: string | null } | null;
  usage: TariffUsage;
}

/** A component as a request carries it: every field its kind does not use is null. */
export interface TariffComponentInput {
  kind: TariffComponentKind;
  name: string;
  rate: number | null;
  rateNight: number | null;
  dayFrom: number | null;
  dayTo: number | null;
  weekdaysOnly: boolean | null;
  tiers: TariffTier[] | null;
  rateReduced: number | null;
  rateBusiness: number | null;
}

export interface TariffVersionInput {
  validFrom: string;
  validTo: string | null;
  components: TariffComponentInput[];
}

/** The header and every version, in one body: POST creates, PUT replaces. */
export interface TariffInput {
  kind: TariffKind;
  name: string;
  description: string | null;
  versions: TariffVersionInput[];
}

export interface TariffListParams {
  kind?: TariffKind;
  status?: TariffStatus;
}

export const tariffsQueryKey = ["energy", "tariffs"] as const;

/** `GET /energy/tariffs`: every tariff, active first, then by kind and name. Needs `energy:tariffs-view`. */
export const tariffsQueryOptions = (params: TariffListParams = {}) =>
  queryOptions({
    queryKey: [...tariffsQueryKey, "list", params],
    queryFn: ({ signal }) => {
      const search = new URLSearchParams();
      if (params.kind) search.set("kind", params.kind);
      if (params.status) search.set("status", params.status);
      return request<TariffSummary[]>(`/api/v1/energy/tariffs${search.size > 0 ? `?${search}` : ""}`, { signal });
    },
  });

/** `GET /energy/tariffs/{id}`: the tariff with every version and component. Needs `energy:tariffs-view`. */
export const tariffQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [...tariffsQueryKey, "item", id],
    queryFn: async ({ signal }) => {
      try {
        return await request<Tariff>(`/api/v1/energy/tariffs/${id}`, { signal });
      } catch (error) {
        if ((error as { status?: number }).status === 404) throw new NotFoundError(`Tariff ${id} does not exist`);
        throw error;
      }
    },
  });

const json = (method: string, body: unknown) => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

/** `POST /energy/tariffs`. Needs `energy:tariffs-manage` and `energy:tariffs-view`. */
export const createTariff = (input: TariffInput) => request<Tariff>("/api/v1/energy/tariffs", json("POST", input));

/** `PUT /energy/tariffs/{id}`: the header and every version replaced; the kind cannot change. */
export const updateTariff = (id: number, input: TariffInput) =>
  request<Tariff>(`/api/v1/energy/tariffs/${id}`, json("PUT", input));

/** `POST /energy/tariffs/{id}/archive`: idempotent. Needs `energy:tariffs-manage`. */
export const archiveTariff = (id: number) =>
  request<Tariff>(`/api/v1/energy/tariffs/${id}/archive`, { method: "POST" });

/** `DELETE /energy/tariffs/{id}`: 409 "Tariff in use" while a supply period or a metering point names it. */
export const deleteTariff = (id: number) => request<void>(`/api/v1/energy/tariffs/${id}`, { method: "DELETE" });

/**
 * `PUT /energy/metering-points/{id}/supply-periods/{periodId}`: a period's
 * power tariff and cadence, nothing else of it. Needs `energy:supply-periods-manage`.
 */
export const updateSupplyPeriodTerms = (
  meteringPointId: number,
  periodId: number,
  input: { tariffId: number | null; cadence: BillingCadence },
) =>
  request<SupplyPeriod>(
    `/api/v1/energy/metering-points/${meteringPointId}/supply-periods/${periodId}`,
    json("PUT", input),
  );

/**
 * After a tariff write: every list and the tariff itself, and the metering
 * points and their supply periods, which print the tariff's name.
 */
export const invalidateTariffs = (client: QueryClient) =>
  Promise.all([
    client.invalidateQueries({ queryKey: tariffsQueryKey }),
    client.invalidateQueries({ queryKey: ["energy", "metering-points"] }),
    client.invalidateQueries({ queryKey: ["energy", "customers"] }),
  ]);
