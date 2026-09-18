import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { PaginatedResponse, TimeEntry } from "./entries";
import { request } from "./request";

type Schemas = components["schemas"];

/** One person's submitted entries in one week that the caller may approve. */
export type TimeApprovalGroup = Omit<Schemas["TimeApprovalGroup"], "entries"> & { entries: TimeEntry[] };

export const APPROVALS_PAGE_SIZE = 25;

/** The approval queue, oldest week first. 403 unless the caller approves anything at all. */
export const approvalsQueryOptions = (page: number) =>
  queryOptions({
    queryKey: ["time", "approvals", page],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ page: String(page), pageSize: String(APPROVALS_PAGE_SIZE) });
      return request<PaginatedResponse<TimeApprovalGroup>>(`/api/v1/time/approvals?${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });

const post = (path: string, body: unknown): Promise<TimeEntry[]> =>
  request<TimeEntry[]>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

/** All or nothing: one entry the caller may not approve refuses the batch. */
export const approveTimeEntries = (ids: number[]) => post("/api/v1/time/entries/approve", { ids });

/** The reason is required and goes to the owner with every entry. */
export const rejectTimeEntries = (ids: number[], reason: string) =>
  post("/api/v1/time/entries/reject", { ids, reason });

/** Approved entries back to draft; an invoiced entry never goes back. */
export const unapproveTimeEntries = (ids: number[]) => post("/api/v1/time/entries/unapprove", { ids });
