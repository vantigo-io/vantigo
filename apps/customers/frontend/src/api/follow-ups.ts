import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { PaginatedResponse } from "./customers";
import { request } from "./request";
import { normalizeFollowUp, type TimelineFollowUp } from "./timeline";

/**
 * The Follow-ups page's two filters (follow-ups design D3). The API also accepts
 * a bare user id for `assignee`; the page deliberately offers only these two,
 * the same choice the customer list's Owner filter makes — a uuid in the URL
 * would narrow the rows by something the Select cannot show.
 */
export type FollowUpAssigneeFilter = "me" | "none";
export type FollowUpState = "open" | "overdue" | "done" | "all";

export const FOLLOW_UP_ASSIGNEES: readonly FollowUpAssigneeFilter[] = ["me", "none"];
export const FOLLOW_UP_STATES: readonly FollowUpState[] = ["open", "overdue", "done", "all"];

/** One row of the list: the entry, its customer, and the follow-up itself. */
export interface FollowUpRow {
  entryId: number;
  customerId: number;
  customerName: string;
  eventType: string;
  occurredOn: string;
  note: string | null;
  followUp: TimelineFollowUp;
}

type RawFollowUpRow = Omit<FollowUpRow, "note" | "followUp"> & {
  note?: string | null;
  followUp: Parameters<typeof normalizeFollowUp>[0];
};

export interface FollowUpsQueryParams {
  page: number;
  pageSize?: number;
  assignee: FollowUpAssigneeFilter;
  state: FollowUpState;
  customerId?: number;
}

/**
 * The search a URL carries, turned into the params both the route's loader and
 * the page's own query build — one function, so a prefetch cannot key
 * differently from the read it is meant to warm (the customer list's
 * `customersListParams` is the same seam for the same reason).
 */
export const followUpsListParams = (search: {
  page?: number;
  assignee?: FollowUpAssigneeFilter;
  state?: FollowUpState;
  customerId?: number;
}): FollowUpsQueryParams => ({
  page: search.page ?? 1,
  assignee: search.assignee ?? "me",
  state: search.state ?? "open",
  customerId: search.customerId,
});

export const followUpsQueryOptions = (params: FollowUpsQueryParams) =>
  queryOptions({
    queryKey: ["customers", "follow-ups", params],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({
        page: String(params.page),
        pageSize: String(params.pageSize ?? 25),
        assignee: params.assignee,
        state: params.state,
      });
      if (params.customerId) query.set("customerId", String(params.customerId));
      const answered = await request<PaginatedResponse<RawFollowUpRow>>(`/api/v1/customers/follow-ups?${query}`, {
        signal,
      });
      return {
        ...answered,
        data: answered.data.map((raw) => ({
          ...raw,
          note: raw.note ?? null,
          // followUp is required on this shape — carrying one is what put the
          // row on the list — so the normaliser's null branch is unreachable
          // here; it is called anyway so `assignee` and `doneAt` get the same
          // absent-to-null treatment they get on an entry.
          followUp: normalizeFollowUp(raw.followUp) as TimelineFollowUp,
        })),
      };
    },
    placeholderData: keepPreviousData,
  });
