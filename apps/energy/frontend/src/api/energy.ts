import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { NotFoundError, request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";

export type PriceArea = "NO1" | "NO2" | "NO3" | "NO4" | "NO5";
export type ConnectionStatus = "New" | "Connected" | "Disconnected";
export type ConsumptionQuality = "Measured" | "Estimated" | "Corrected" | "Manual";
export type ConsumptionSource = "Elhub" | "Manual";
export type SupplyPeriodStatus = "Active" | "Ended" | "Cancelled";
export type ConsumptionResolution = "hour" | "day" | "month";

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
}

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

export const supplyPeriodsQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["energy", "metering-points", id, "supply-periods"],
    queryFn: ({ signal }) => request<SupplyPeriod[]>(`/api/v1/energy/metering-points/${id}/supply-periods`, { signal }),
  });

export const assignSupplyPeriod = (id: number, input: { customerId: number; start: string }) =>
  request<SupplyPeriod>(`/api/v1/energy/metering-points/${id}/supply-periods`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export interface SwitchSupplyPeriodResponse {
  endedPeriod: SupplyPeriod | null;
  newPeriod: SupplyPeriod;
}

export const switchSupplyPeriod = (id: number, input: { customerId: number; start: string }) =>
  request<SwitchSupplyPeriodResponse>(`/api/v1/energy/metering-points/${id}/supply-periods/switch`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ customerId: input.customerId, switchAt: input.start }),
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
