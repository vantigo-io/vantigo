import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { MilestoneStatus } from "../lib/milestones";
import { request } from "./request";

export type { MilestoneStatus } from "../lib/milestones";

type Schemas = components["schemas"];

/**
 * The contract declares the milestone status as a plain string (OpenAPI 3.0
 * without enum members), so the generated row is narrowed here to the exact
 * strings the backend writes. Everything else is the generated shape.
 *
 * Optional fields are *absent*, never null: `amount` and `percent` are
 * mutually exclusive, `currency` only outlives a project on a cancelled
 * milestone, and `effectiveAmount` is missing in exactly one case — a
 * cancelled percent milestone whose project no longer has a fixed price.
 */
export type BillingMilestone = Omit<Schemas["BillingMilestoneResponse"], "status"> & { status: MilestoneStatus };

export type BillingMilestoneCapabilities = Schemas["BillingMilestoneCapabilities"];
export type BillingMilestonePerson = Schemas["BillingMilestonePerson"];
export type BillingMilestoneTotals = Schemas["BillingMilestonePlanTotals"];

/** The project's milestones in manual order, cancelled ones last, and what they add up to. */
export type BillingMilestonePlan = Omit<Schemas["BillingMilestonePlanResponse"], "milestones"> & {
  milestones: BillingMilestone[];
};

export type BillingMilestoneInput = Schemas["BillingMilestoneRequest"];

/** An update carries every field plus the revision the milestone was read at; position and status are not part of it. */
export type BillingMilestoneUpdateInput = Schemas["BillingMilestoneUpdateRequest"];

export type BillingMilestonePositionInput = Schemas["BillingMilestonePositionRequest"];

export type BillingMilestoneStatusInput = Omit<Schemas["BillingMilestoneStatusRequest"], "status"> & {
  status: MilestoneStatus;
};

/**
 * The project's invoice plan. Every milestone query key starts
 * `["projects", "milestones"]`, so the blanket `["projects"]` invalidation
 * every write in this package does still reaches it.
 *
 * The API answers 403 to a caller who may see the project but not its
 * amounts, so this is only ever asked for once `canSeeFinancials` says yes.
 */
export const milestonePlanQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["projects", "milestones", "plan", projectId],
    queryFn: ({ signal }) => request<BillingMilestonePlan>(`/api/v1/projects/${projectId}/milestones`, { signal }),
  });

/** A new milestone is appended to the plan, always `planned`. */
export const createMilestone = (projectId: number, input: BillingMilestoneInput): Promise<BillingMilestone> =>
  request<BillingMilestone>(`/api/v1/projects/${projectId}/milestones`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** A full replace: a field left out is cleared, and a stale revision answers 409. */
export const updateMilestone = (milestoneId: number, input: BillingMilestoneUpdateInput): Promise<BillingMilestone> =>
  request<BillingMilestone>(`/api/v1/projects/milestones/${milestoneId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** Only a milestone still planned and never moved may go; anything else is cancelled instead. */
export const deleteMilestone = (milestoneId: number): Promise<void> =>
  request<void>(`/api/v1/projects/milestones/${milestoneId}`, { method: "DELETE" });

/**
 * Where the milestone should sit. The whole plan is renumbered 1..n, and the
 * revision the caller read is carried but never bumped — a reorder leaves
 * everybody's open edit form valid.
 */
export const moveMilestone = (milestoneId: number, input: BillingMilestonePositionInput): Promise<BillingMilestone> =>
  request<BillingMilestone>(`/api/v1/projects/milestones/${milestoneId}/position`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/**
 * One move through the status flow. A refused move answers 400 on `status`;
 * which moves are allowed is what each milestone's `capabilities` already say.
 */
export const setMilestoneStatus = (
  milestoneId: number,
  input: BillingMilestoneStatusInput,
): Promise<BillingMilestone> =>
  request<BillingMilestone>(`/api/v1/projects/milestones/${milestoneId}/status`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
