import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

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
  followUp?: TimelineFollowUpInput | null;
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
  /** The author's name, snapshotted at write time (design D1). Blank for entries older than the change, and for a write with no user principal. */
  actorDisplay?: string | null;
  /** What kind of author it was: `user`, `system` (a generated event) or `unattributed`. Decides the label, since the display's sentinels are English — see `lib/actor-label.ts`. */
  actorKind: string;
  /** What happens next on this entry (follow-ups design D1). Null when the entry carries none. */
  followUp: TimelineFollowUp | null;
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
  /** The same D1 actor kind the entry itself carries, and read the same way. */
  actorKind: string;
  /** The follow-up as it stood at this revision (follow-ups design D1). Null when the entry carried none at this revision. */
  followUp: TimelineFollowUp | null;
}
export interface TimelineRevisionResponse {
  data: TimelineRevision[];
}

/** A follow-up's assignee, named by the server from the user directory. */
export interface TimelineAssignee {
  userId: string;
  displayName: string;
  /** False for an account disabled or removed since it was given the follow-up. */
  active: boolean;
}

/** What happens next on an entry (follow-ups design D1). */
export interface TimelineFollowUp {
  dueOn: string;
  assignee: TimelineAssignee | null;
  doneAt: string | null;
}

/** The follow-up to send. `null`, or omitting the field, clears it. */
export interface TimelineFollowUpInput {
  dueOn: string;
  assigneeUserId?: string;
}

/**
 * The wire shape of a follow-up: every optional field is ABSENT rather than
 * null when it has no value, which is what this API does everywhere. Turning
 * that into null here is what lets every component read `entry.followUp` and
 * `followUp.assignee` without a second thought about which of the two shapes
 * answered.
 */
type RawTimelineFollowUp = { dueOn: string; assignee?: TimelineAssignee | null; doneAt?: string | null };
type RawTimelineEntry = Omit<TimelineEntry, "followUp"> & { followUp?: RawTimelineFollowUp | null };
type RawTimelineRevision = Omit<TimelineRevision, "followUp"> & { followUp?: RawTimelineFollowUp | null };

export const normalizeFollowUp = (raw?: RawTimelineFollowUp | null): TimelineFollowUp | null =>
  raw ? { dueOn: raw.dueOn, assignee: raw.assignee ?? null, doneAt: raw.doneAt ?? null } : null;

export const normalizeTimelineEntry = (raw: RawTimelineEntry): TimelineEntry => ({
  ...raw,
  followUp: normalizeFollowUp(raw.followUp),
});

export const normalizeTimelinePage = (page: {
  data: RawTimelineEntry[];
  nextCursor?: string | null;
}): TimelinePage => ({
  data: page.data.map(normalizeTimelineEntry),
  nextCursor: page.nextCursor ?? null,
});

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
  const page = await request<{ data: RawTimelineEntry[]; nextCursor: string | null }>(
    `/api/v1/customers/${customerId}/timeline?${params}`,
    { signal },
  );
  return normalizeTimelinePage(page);
}
export async function createTimelineEntry(customerId: number, input: TimelineInput) {
  return normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    })) as RawTimelineEntry,
  );
}
export async function updateTimelineEntry(
  customerId: number,
  id: TimelineEntry["id"],
  input: TimelineInput,
  expectedRevision: number,
) {
  return normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...input, expectedRevision }),
    })) as RawTimelineEntry,
  );
}
export async function deleteTimelineEntry(customerId: number, entry: TimelineEntry) {
  return request(`/api/v1/customers/${customerId}/timeline/${entry.id}?expectedRevision=${entry.currentRevision}`, {
    method: "DELETE",
  });
}

/**
 * Ticks an entry's follow-up done, or reopens it. Neither sends an
 * expectedRevision, and that is the contract's own decision (follow-ups design
 * D1): a tick comes from a list and must not conflict with somebody editing the
 * note. Both answer the whole entry, so a caller can read the fresh revision
 * straight off the response.
 */
export const markFollowUpDone = async (customerId: number, id: TimelineEntry["id"]) =>
  normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline/${id}/follow-up/done`, {
      method: "POST",
    })) as RawTimelineEntry,
  );

export const reopenFollowUp = async (customerId: number, id: TimelineEntry["id"]) =>
  normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline/${id}/follow-up/done`, {
      method: "DELETE",
    })) as RawTimelineEntry,
  );

export const timelineRevisionsQueryOptions = (customerId: number, id: TimelineEntry["id"]) =>
  queryOptions({
    queryKey: ["customers", customerId, "timeline", id, "revisions"],
    queryFn: async () => {
      const response = (await request(`/api/v1/customers/${customerId}/timeline/${id}/revisions`)) as {
        data: RawTimelineRevision[];
      };
      return response.data.map((raw) => ({ ...raw, followUp: normalizeFollowUp(raw.followUp) }));
    },
  });
