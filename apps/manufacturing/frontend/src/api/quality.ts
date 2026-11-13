import { queryOptions, useQueryClient } from "@tanstack/react-query";
import { appUrl, useLiveTopic } from "@vantigo/frontend-shell";
import type { components } from "../api-schema";
import { downloadCsvFrom } from "./reports";
import {
  currentStationToken,
  handleUnauthorized,
  json,
  MANUFACTURING_QUERY_KEY,
  queryString,
  refusalOf,
  request,
  send,
  stationAnswered,
  stationInit,
} from "./request";

type Schemas = components["schemas"];

export type ManufacturingCheckKind = Schemas["ManufacturingCheckKind"];
export type ManufacturingCheckFrequency = Schemas["ManufacturingCheckFrequency"];
export type ManufacturingCheckTemplate = Schemas["ManufacturingCheckTemplate"];
export type ManufacturingCheckTemplateInput = Schemas["ManufacturingCheckTemplateInput"];
export type ManufacturingWorkOrderCheck = Schemas["ManufacturingWorkOrderCheck"];
export type ManufacturingCheckResult = Schemas["ManufacturingCheckResult"];
export type ManufacturingCheckResultInput = Schemas["ManufacturingCheckResultInput"];
export type ManufacturingCheckResultValue = Schemas["ManufacturingCheckResultValue"];
export type ManufacturingCheckPhoto = Schemas["ManufacturingCheckPhoto"];
export type ManufacturingDeviation = Schemas["ManufacturingQualityDeviation"];
export type ManufacturingDeviationInput = Schemas["ManufacturingQualityDeviationInput"];
export type ManufacturingDeviationStatus = Schemas["ManufacturingQualityDeviationStatus"];
export type ManufacturingDeviationDisposition = Schemas["ManufacturingDeviationDisposition"];
export type ManufacturingDeviationCloseInput = Schemas["ManufacturingDeviationCloseInput"];
export type ManufacturingMissingCheck = Schemas["ManufacturingMissingCheck"];

/** What a check asks (manufacturing phase 2 M95). */
export const checkKinds: readonly ManufacturingCheckKind[] = ["instruction", "pass_fail", "measure", "photo"];
/** When a check is recorded (M95). */
export const checkFrequencies: readonly ManufacturingCheckFrequency[] = ["each_work_order", "first_piece"];
/** A deviation's states and dispositions (M99). */
export const deviationStatuses: readonly ManufacturingDeviationStatus[] = ["open", "closed"];
export const deviationDispositions: readonly ManufacturingDeviationDisposition[] = [
  "use_as_is",
  "rework",
  "scrap",
  "return",
];
/** At most this many checks an operation (M95). */
export const MAX_CHECKS_PER_OPERATION = 20;
/** A page of the deviations list. */
export const DEVIATION_PAGE_SIZE = 50;

/** Records a check's result (M97); answers the work order view. */
export const recordCheckResult = (workOrderId: number, checkId: number, input: ManufacturingCheckResultInput) =>
  send<Schemas["ManufacturingWorkOrderView"]>(
    `/api/v1/manufacturing/work-orders/${workOrderId}/checks/${checkId}/result`,
    json("POST", input),
  );

/** Uploads a check's photo (M97): the tablet's camera, through a file input. */
export const uploadCheckPhoto = async (workOrderId: number, file: File): Promise<ManufacturingCheckPhoto> => {
  const body = new FormData();
  body.append("file", file);
  const token = currentStationToken();
  const response = await fetch(appUrl(`/api/v1/manufacturing/work-orders/${workOrderId}/photos`), {
    ...stationInit({ method: "POST", body }),
    credentials: "include",
  });
  if (response.status === 401) await handleUnauthorized(response, token);
  else stationAnswered(token);
  if (!response.ok) throw await refusalOf(response);
  return (await response.json()) as ManufacturingCheckPhoto;
};

/** Where a check's photo is served. */
export const checkPhotoUrl = (workOrderId: number, photoId: number) =>
  appUrl(`/api/v1/manufacturing/work-orders/${workOrderId}/photos/${photoId}`);

export interface DeviationFilter {
  status?: ManufacturingDeviationStatus;
  orderId?: number;
  variantId?: number;
  lotId?: number;
  from?: string;
  to?: string;
  offset?: number;
}

const deviationQuery = (filter: DeviationFilter, format?: "csv") =>
  queryString({
    status: filter.status,
    orderId: filter.orderId,
    variantId: filter.variantId,
    lotId: filter.lotId,
    from: filter.from,
    to: filter.to,
    limit: format ? undefined : DEVIATION_PAGE_SIZE,
    offset: format ? undefined : filter.offset,
    format,
  });

/** A page of deviations, newest first (M99). */
export const deviationsQueryOptions = (filter: DeviationFilter = {}) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "deviations", filter],
    queryFn: ({ signal }) =>
      request<Schemas["ManufacturingQualityDeviationList"]>(
        `/api/v1/manufacturing/deviations${deviationQuery(filter)}`,
        { signal },
      ),
  });

/** One deviation. */
export const deviationQueryOptions = (id: number) =>
  queryOptions({
    queryKey: [MANUFACTURING_QUERY_KEY, "deviation", id],
    queryFn: ({ signal }) => request<ManufacturingDeviation>(`/api/v1/manufacturing/deviations/${id}`, { signal }),
  });

/** Every deviation the filter matches as a CSV file. */
export const downloadDeviationsCsv = (filter: DeviationFilter) =>
  downloadCsvFrom(`/api/v1/manufacturing/deviations${deviationQuery(filter, "csv")}`, "manufacturing-deviations.csv");

/** Reports a deviation by hand (M99). */
export const createDeviation = (input: ManufacturingDeviationInput) =>
  send<ManufacturingDeviation>("/api/v1/manufacturing/deviations", json("POST", input));

/** Closes a deviation with its disposition and note (M99). */
export const closeDeviation = (id: number, input: ManufacturingDeviationCloseInput) =>
  send<ManufacturingDeviation>(`/api/v1/manufacturing/deviations/${id}/close`, json("POST", input));

/** Releases the holds a deviation closed as scrap or return kept (M99). */
export const releaseDeviationHolds = (id: number, revision: number) =>
  send<ManufacturingDeviation>(`/api/v1/manufacturing/deviations/${id}/release`, json("POST", { revision }));

/** Listens to the deviations' topic (§17) and reads every deviation query again on an event. */
export function useDeviationsLive(): boolean {
  const client = useQueryClient();
  return useLiveTopic("manufacturing.deviations", () => {
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "deviations"] });
    void client.invalidateQueries({ queryKey: [MANUFACTURING_QUERY_KEY, "deviation"] });
  });
}

/** The checks shown first: the first-piece checks without a result, then the rest, each by sequence (M101). */
export const orderedChecks = (checks: readonly ManufacturingWorkOrderCheck[]): ManufacturingWorkOrderCheck[] => {
  const first = (check: ManufacturingWorkOrderCheck) => check.frequency === "first_piece" && check.result === null;
  return [...checks].sort((a, b) => Number(first(b)) - Number(first(a)) || a.sequence - b.sequence);
};
