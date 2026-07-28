import { queryOptions } from "@tanstack/react-query";

export type TimelineProvenance = "manual" | "generated";
export type TimelineSourceFilter = "all" | TimelineProvenance;
export interface TimelineFilters {
  provenance: TimelineSourceFilter;
  eventTypes: string[];
  occurredFrom: string | null;
  occurredTo: string | null;
}
export const defaultTimelineFilters: TimelineFilters = {
  provenance: "all",
  eventTypes: [],
  occurredFrom: null,
  occurredTo: null,
};
export const normalizeTimelineFilters = (filters: Partial<TimelineFilters> = {}): TimelineFilters => ({
  provenance: filters.provenance === "manual" || filters.provenance === "generated" ? filters.provenance : "all",
  eventTypes: [...new Set(filters.eventTypes ?? [])].sort(),
  occurredFrom: filters.occurredFrom || null,
  occurredTo: filters.occurredTo || null,
});
export type TimelineInput = {
  eventType: string;
  occurredOn: string;
  occurredAt?: string;
  note: string;
  sourceUrl?: string;
};
export interface TimelineEntry {
  eventType: string;
  occurredOn: string;
  note: string | null;
  id: number | string;
  provenance: TimelineProvenance;
  producer: string;
  summary: string | null;
  occurredAt: string | null;
  sourceUrl: string | null;
  payload: unknown;
  currentRevision: number;
  createdAt: string;
  updatedAt: string;
}
export interface TimelinePage {
  data: TimelineEntry[];
  nextCursor: string | null;
}
export interface TimelineRevision {
  eventType: string;
  occurredOn: string;
  note: string;
  revision: number;
  action: "create" | "update" | "delete" | string;
  occurredAt: string | null;
  sourceUrl: string | null;
  changedAt: string;
  actorDisplayName: string;
}
export interface TimelineRevisionResponse {
  data: TimelineRevision[];
}

const request = async (url: string, init?: RequestInit) => {
  const response = await fetch(url, init);
  if (!response.ok) {
    const error = new Error(`Request failed (HTTP ${response.status}`);
    Object.assign(error, { status: response.status });
    throw error;
  }
  if (response.status === 204) return undefined;
  return response.json();
};

export async function fetchTimeline(
  customerId: number,
  cursor?: string,
  signal?: AbortSignal,
  filters: TimelineFilters = defaultTimelineFilters,
): Promise<TimelinePage> {
  const params = new URLSearchParams({ limit: "25" });
  if (cursor) params.set("cursor", cursor);
  const normalized = normalizeTimelineFilters(filters);
  if (normalized.provenance !== "all") params.set("provenance", normalized.provenance);
  normalized.eventTypes.forEach((eventType) => {
    params.append("eventType", eventType);
  });
  if (normalized.occurredFrom) params.set("occurredFrom", normalized.occurredFrom);
  if (normalized.occurredTo) params.set("occurredTo", normalized.occurredTo);
  return request(`/api/v1/customers/${customerId}/timeline?${params}`, { signal });
}
export async function createTimelineEntry(customerId: number, input: TimelineInput) {
  return request(`/api/v1/customers/${customerId}/timeline`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  }) as Promise<TimelineEntry>;
}
export async function updateTimelineEntry(
  customerId: number,
  id: TimelineEntry["id"],
  input: TimelineInput,
  expectedRevision: number,
) {
  return request(`/api/v1/customers/${customerId}/timeline/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...input, expectedRevision }),
  }) as Promise<TimelineEntry>;
}
export async function deleteTimelineEntry(customerId: number, entry: TimelineEntry) {
  return request(`/api/v1/customers/${customerId}/timeline/${entry.id}?expectedRevision=${entry.currentRevision}`, {
    method: "DELETE",
  });
}
export const timelineRevisionsQueryOptions = (customerId: number, id: TimelineEntry["id"]) =>
  queryOptions({
    queryKey: ["customers", customerId, "timeline", id, "revisions"],
    queryFn: async () => {
      const response = (await request(
        `/api/v1/customers/${customerId}/timeline/${id}/revisions`,
      )) as TimelineRevisionResponse;
      return response.data;
    },
  });
