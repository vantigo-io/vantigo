import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { NotFoundError, request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";

export type PriceArea = "NO1" | "NO2" | "NO3" | "NO4" | "NO5";
export type ConnectionStatus = "New" | "Connected" | "Disconnected";
export type ConsumptionQuality = "Measured" | "Estimated" | "Corrected" | "Manual";
export type ConsumptionSource = "Elhub" | "Manual" | "Import";
export type SupplyPeriodStatus = "Active" | "Ended" | "Cancelled";
export type ConsumptionResolution = "hour" | "day" | "month";
export type ElectricityTaxClass = "ordinary" | "reduced" | "exempt";
export type VatTreatment = "standard" | "exempt_household_north";
export type BillingCadence = "monthly" | "quarterly" | "yearly";
export const BILLING_CADENCES: readonly BillingCadence[] = ["monthly", "quarterly", "yearly"];
export const ELECTRICITY_TAX_CLASSES: readonly ElectricityTaxClass[] = ["ordinary", "reduced", "exempt"];

export interface MeteringPointAddress {
  streetAddress: string;
  postalCode: string;
  city: string;
  countryCode: string;
}

export interface MeteringPoint {
  id: number;
  gsrn: string;
  meterNumber: string | null;
  address: MeteringPointAddress;
  priceArea: PriceArea;
  gridArea?: string;
  expectedAnnualConsumptionKwh?: number;
  latitude?: number;
  longitude?: number;
  connectionStatus: ConnectionStatus;
  createdAt: string;
  updatedAt: string;
  /**
   * The billing facts (energy consumption billing design D3, D4): always
   * sent, optional because the recorded .NET exchanges predate them — read a
   * missing one with `billingFacts`.
   */
  gridTariffId?: number | null;
  gridTariffName?: string | null;
  household?: boolean;
  electricityTaxClass?: ElectricityTaxClass;
  vatTreatment?: VatTreatment;
}

/** A point's billing facts, a missing one read as none, false, ordinary and standard. */
export const billingFacts = (point: MeteringPoint) => ({
  gridTariffId: point.gridTariffId ?? null,
  gridTariffName: point.gridTariffName ?? null,
  household: point.household ?? false,
  electricityTaxClass: point.electricityTaxClass ?? "ordinary",
  vatTreatment: point.vatTreatment ?? "standard",
});

export interface MeteringPointInput {
  gsrn: string;
  meterNumber: string;
  address: MeteringPointAddress;
  priceArea: PriceArea;
  gridArea?: string;
  expectedAnnualConsumptionKwh?: number;
  latitude?: number;
  longitude?: number;
  connectionStatus?: ConnectionStatus;
  /** The PUT is a full replace: absent is none, false and ordinary, so the form always sends the three. */
  gridTariffId: number | null;
  household: boolean;
  electricityTaxClass: ElectricityTaxClass;
}

export type MeteringPointUpdateInput = Omit<MeteringPointInput, "meterNumber">;

export interface Meter {
  id: number;
  meteringPointId: number;
  meterNumber: string;
  installedAt: string;
  removedAt?: string | null;
}

export interface ConsumptionInterval {
  id: number;
  meteringPointId: number;
  start: string;
  end: string;
  quantityKwh: number;
  quality: ConsumptionQuality;
  source: ConsumptionSource;
  receivedAt: string;
}

export interface ConsumptionInput {
  start: string;
  end: string;
  quantityKwh: number;
}

export interface ConsumptionAggregate {
  bucketStart: string;
  bucketEnd: string;
  quantityKwh: number;
  intervalCount: number;
  hasEstimated: boolean;
  meteringPointId?: number;
}

export interface SupplyPeriod {
  id: number;
  meteringPointId: number;
  customerId: number;
  start: string;
  end?: string | null;
  status: SupplyPeriodStatus;
  /** Always sent, optional for the same reason as a point's billing facts: missing is none and monthly. */
  tariffId?: number | null;
  tariffName?: string | null;
  cadence?: BillingCadence;
}

/** What a supply period starts with besides the customer and the date. */
export interface SupplyTermsInput {
  tariffId: number | null;
  cadence: BillingCadence;
}

export interface CustomerMeteringPoint {
  meteringPoint: MeteringPoint;
  supplyPeriods: SupplyPeriod[];
}

export interface PaginationMetadata {
  page: number;
  pageSize: number;
  totalCount: number;
  totalPages: number;
  hasNextPage?: boolean;
  hasPreviousPage?: boolean;
}

export interface MeteringPointListResponse {
  data: MeteringPoint[];
  pagination: PaginationMetadata;
}

export interface MeteringPointQueryParams {
  page?: number;
  pageSize?: number;
  search?: string;
  /** Only the points with no customer now (energy import design D9). */
  withoutCustomer?: boolean;
}

const queryString = (params: object) => {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params))
    if (value !== undefined && value !== "") search.set(key, String(value));
  return search.size > 0 ? `?${search}` : "";
};

export const meteringPointsQueryOptions = (params: MeteringPointQueryParams) =>
  queryOptions({
    queryKey: ["energy", "metering-points", params],
    queryFn: ({ signal }) =>
      request<MeteringPointListResponse>(`/api/v1/energy/metering-points${queryString(params)}`, { signal }),
    placeholderData: keepPreviousData,
  });

const fetchMeteringPoint = async (id: number, signal?: AbortSignal) => {
  try {
    return await request<MeteringPoint>(`/api/v1/energy/metering-points/${id}`, { signal });
  } catch (error) {
    if ((error as { status?: number }).status === 404) throw new NotFoundError(`Metering point ${id} does not exist`);
    throw error;
  }
};

export const meteringPointQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["energy", "metering-points", id],
    queryFn: ({ signal }) => fetchMeteringPoint(id, signal),
  });

export const createMeteringPoint = (input: MeteringPointInput) =>
  request<MeteringPoint>("/api/v1/energy/metering-points", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateMeteringPoint = (id: number, input: MeteringPointUpdateInput) =>
  request<MeteringPoint>(`/api/v1/energy/metering-points/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const metersQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["energy", "metering-points", id, "meters"],
    queryFn: ({ signal }) => request<Meter[]>(`/api/v1/energy/metering-points/${id}/meters`, { signal }),
  });

export const replaceMeter = (id: number, input: { meterNumber: string; installedAt: string }) =>
  request<Meter>(`/api/v1/energy/metering-points/${id}/meters`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const consumptionQueryOptions = (id: number, from: string, to: string) =>
  queryOptions({
    queryKey: ["energy", "metering-points", id, "consumption", { from, to }],
    queryFn: ({ signal }) =>
      request<ConsumptionInterval[]>(`/api/v1/energy/metering-points/${id}/consumption${queryString({ from, to })}`, {
        signal,
      }),
  });

export const consumptionAggregateQueryOptions = (
  id: number,
  params: { from: string; to: string; resolution: ConsumptionResolution },
) =>
  queryOptions({
    queryKey: ["energy", "metering-points", id, "consumption", "aggregate", params],
    queryFn: ({ signal }) =>
      request<ConsumptionAggregate[]>(
        `/api/v1/energy/metering-points/${id}/consumption/aggregate${queryString(params)}`,
        { signal },
      ),
  });

export const addConsumption = (id: number, input: ConsumptionInput) =>
  request<ConsumptionInterval>(`/api/v1/energy/metering-points/${id}/consumption`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/**
 * The consumption import's mapping (energy import design D8): the 0-based
 * column of each field. `start` and `quantityKwh` are required, and exactly
 * one of `end` and `resolution`; as the server suggests it, a field it found
 * no column for is absent.
 */
export interface ConsumptionImportMapping {
  start?: number;
  end?: number;
  resolution?: "PT1H" | "PT15M";
  quantityKwh?: number;
  quality?: number;
  gsrn?: number;
}

/** One problem of one row: `column` is the file's 0-based column, absent for the row as a whole. */
export interface ConsumptionImportError {
  row: number;
  column?: number;
  message: string;
}

/**
 * What the consumption import read and did: the file's facts on every call,
 * and — when a mapping was sent (`evaluated`) — the counts, the first 200
 * errors and the range the readings cover. `imported` is true only when the
 * readings were written.
 */
export interface ConsumptionImportResult {
  dryRun: boolean;
  evaluated: boolean;
  imported: boolean;
  encoding: string;
  separator: string;
  header: string[];
  sample: { row: number; cells: string[] }[];
  rows: number;
  suggested: ConsumptionImportMapping;
  new: number;
  replacing: number;
  unchanged: number;
  errorCount: number;
  errors: ConsumptionImportError[];
  rangeStart?: string;
  rangeEnd?: string;
}

/** The server's own cap: a larger file is refused here rather than sent to be refused. */
export const MAX_CONSUMPTION_IMPORT_FILE_BYTES = 10 * 1024 * 1024;

/**
 * `POST /energy/metering-points/{id}/consumption/import`: the file, and the
 * mapping when there is one. Without a mapping the file is only read; with
 * one, `dryRun` checks it and `dryRun: false` writes every row, or — answered
 * 422 — nothing. Needs `energy:import`, `energy:consumption-manage` and
 * `energy:consumption-view`.
 */
export const importConsumption = (
  id: number,
  file: File,
  mapping: ConsumptionImportMapping | null,
  dryRun = true,
): Promise<ConsumptionImportResult> => {
  const form = new FormData();
  form.append("file", file, file.name);
  if (mapping) form.append("mapping", JSON.stringify(mapping));
  return request<ConsumptionImportResult>(`/api/v1/energy/metering-points/${id}/consumption/import?dryRun=${dryRun}`, {
    method: "POST",
    body: form,
  });
};

export const supplyPeriodsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["energy", "metering-points", id, "supply-periods"],
    queryFn: ({ signal }) => request<SupplyPeriod[]>(`/api/v1/energy/metering-points/${id}/supply-periods`, { signal }),
  });

export const assignSupplyPeriod = (
  id: number,
  input: { customerId: number; start: string } & Partial<SupplyTermsInput>,
) =>
  request<SupplyPeriod>(`/api/v1/energy/metering-points/${id}/supply-periods`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export interface SwitchSupplyPeriodResponse {
  endedPeriod: SupplyPeriod | null;
  newPeriod: SupplyPeriod;
}

export const switchSupplyPeriod = (
  id: number,
  input: { customerId: number; start: string } & Partial<SupplyTermsInput>,
) =>
  request<SwitchSupplyPeriodResponse>(`/api/v1/energy/metering-points/${id}/supply-periods/switch`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      customerId: input.customerId,
      switchAt: input.start,
      tariffId: input.tariffId,
      cadence: input.cadence,
    }),
  });

export const endSupplyPeriod = (id: number, periodId: number, end: string) =>
  request<SupplyPeriod>(`/api/v1/energy/metering-points/${id}/supply-periods/${periodId}/end`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ end }),
  });

export const customerMeteringPointsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["energy", "customers", customerId, "metering-points"],
    queryFn: ({ signal }) =>
      request<CustomerMeteringPoint[]>(`/api/v1/energy/customers/${customerId}/metering-points`, { signal }),
  });

export const customerConsumptionQueryOptions = (
  customerId: number,
  meteringPointId: number,
  from: string,
  to: string,
) =>
  queryOptions({
    queryKey: ["energy", "customers", customerId, "consumption", { meteringPointId, from, to }],
    queryFn: ({ signal }) =>
      request<ConsumptionInterval[]>(
        `/api/v1/energy/customers/${customerId}/consumption${queryString({ meteringPointId, from, to })}`,
        { signal },
      ),
  });

export const customerConsumptionAggregateQueryOptions = (
  customerId: number,
  params: { meteringPointId?: number; from: string; to: string; resolution: ConsumptionResolution },
) =>
  queryOptions({
    queryKey: ["energy", "customers", customerId, "consumption", "aggregate", params],
    queryFn: ({ signal }) =>
      request<ConsumptionAggregate[]>(
        `/api/v1/energy/customers/${customerId}/consumption/aggregate${queryString(params)}`,
        { signal },
      ),
  });
