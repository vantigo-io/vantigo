import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { TimeEntryStatus } from "../lib/status";
import { request } from "./request";

export type { TimeEntryStatus } from "../lib/status";
export { ApiValidationError, NotFoundError } from "./request";

type Schemas = components["schemas"];

/**
 * The rate chain step a billable entry's bill rate came from (design D3; the
 * customer's default since the customers bill-rate design, D3).
 */
export type RateSource = "line" | "project" | "customer" | "person" | "none";

/**
 * The contract declares the enumerations as plain strings (OpenAPI 3.0
 * without enum members), so the generated row is narrowed here to the exact
 * strings the backend answers. Everything else is the generated shape.
 */
export type TimeEntry = Omit<Schemas["TimeEntryResponse"], "status" | "rateSource"> & {
  status: TimeEntryStatus;
  rateSource: RateSource;
};

export type TimeEntryCapabilities = Schemas["TimeEntryCapabilities"];
export type TimeEntryBilling = Schemas["TimeEntryBilling"];
export type TimeEntryCost = Schemas["TimeEntryCost"];
export type TimeEntryApprover = Schemas["TimeEntryApprover"];
export type TimeEntryWorkType = Schemas["TimeEntryWorkType"];
export type PaginationMetadata = Schemas["PaginationMetadata"];

export type TimeEntryInput = Schemas["TimeEntryRequest"];

/** An update is a full replace carrying the revision the entry was read at. */
export type TimeEntryUpdateInput = Schemas["TimeEntryUpdateRequest"];

export interface PaginatedResponse<T> {
  data: T[];
  pagination: PaginationMetadata;
}

/** What `GET /time/entries` may be narrowed by. An absent filter is no filter at all. */
export interface TimeEntryFilters {
  userId?: string;
  /** A Monday; the list then holds that week's entries. */
  weekStart?: string;
  projectId?: number;
  status?: TimeEntryStatus;
  page?: number;
  pageSize?: number;
}

const listQuery = (filters: TimeEntryFilters): string => {
  const query = new URLSearchParams();
  if (filters.userId) query.set("userId", filters.userId);
  if (filters.weekStart) query.set("weekStart", filters.weekStart);
  if (filters.projectId !== undefined) query.set("projectId", String(filters.projectId));
  if (filters.status) query.set("status", filters.status);
  if (filters.page !== undefined) query.set("page", String(filters.page));
  if (filters.pageSize !== undefined) query.set("pageSize", String(filters.pageSize));
  const search = query.toString();
  return search ? `?${search}` : "";
};

/**
 * One page of the entries the caller may see. Every query key in the package
 * starts with `"time"`, so the blanket `["time"]` invalidation every write
 * does reaches all of them.
 */
export const timeEntriesQueryOptions = (filters: TimeEntryFilters) =>
  queryOptions({
    queryKey: ["time", "entries", "list", filters],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<TimeEntry>>(`/api/v1/time/entries${listQuery(filters)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const timeEntryQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["time", "entries", "detail", id],
    queryFn: ({ signal }) => request<TimeEntry>(`/api/v1/time/entries/${id}`, { signal }),
  });

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const createTimeEntry = (input: TimeEntryInput): Promise<TimeEntry> =>
  request<TimeEntry>("/api/v1/time/entries", json("POST", input));

/** A full replace: a revision that has moved on answers 409 and the caller is asked to reload. */
export const updateTimeEntry = (id: number, input: TimeEntryUpdateInput): Promise<TimeEntry> =>
  request<TimeEntry>(`/api/v1/time/entries/${id}`, json("PUT", input));

/** Only a draft or rejected entry of the caller's own goes; anything else answers 403. */
export const deleteTimeEntry = (id: number): Promise<void> =>
  request<void>(`/api/v1/time/entries/${id}`, { method: "DELETE" });

/** Submits single drafts; `submitWeek` submits a whole week. */
export const submitTimeEntries = (ids: number[]): Promise<TimeEntry[]> =>
  request<TimeEntry[]>("/api/v1/time/entries/submit", json("POST", { ids }));

/**
 * The update body for an entry as it stands, with the one thing the caller is
 * changing applied. An update is a full replace, so anything left out would
 * be cleared: a new duration from the week grid has to carry the note and the
 * line along with it.
 */
export const timeEntryUpdateFrom = (
  entry: TimeEntry,
  changes: Partial<TimeEntryUpdateInput> = {},
): TimeEntryUpdateInput => ({
  projectId: entry.projectId,
  billingLineId: entry.billingLineId ?? null,
  taskId: entry.taskId ?? null,
  entryDate: entry.entryDate,
  hours: entry.hours,
  startTime: entry.startTime ?? null,
  endTime: entry.endTime ?? null,
  note: entry.note ?? null,
  billable: entry.billable,
  revision: entry.revision,
  // An update is a full replace: an entry logged as a work type has to say so
  // again, or the grid's new hours would make it ordinary hours.
  ...(entry.workType ? { workTypeId: entry.workType.id } : {}),
  ...changes,
});
