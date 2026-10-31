import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { DeliveryMilestoneStatus } from "../lib/delivery-milestones";
import { request } from "./request";

export type { DeliveryMilestoneStatus } from "../lib/delivery-milestones";

type Schemas = components["schemas"];

export type DeliveryMilestone = Omit<Schemas["DeliveryMilestoneResponse"], "status"> & {
  status: DeliveryMilestoneStatus;
};

export type DeliveryMilestoneSummary = Schemas["DeliveryMilestoneSummary"];
export type DeliveryMilestonePerson = Schemas["DeliveryMilestonePerson"];

export type DeliveryMilestoneList = {
  milestones: DeliveryMilestone[];
};

export type DeliveryMilestoneCreateInput = Schemas["DeliveryMilestoneCreateRequest"];
export type DeliveryMilestoneUpdateInput = Schemas["DeliveryMilestoneUpdateRequest"];
export type DeliveryMilestonePositionInput = Schemas["DeliveryMilestonePositionRequest"];
export type DeliveryMilestoneStatusInput = Omit<Schemas["DeliveryMilestoneStatusRequest"], "status"> & {
  status: DeliveryMilestoneStatus;
};

/**
 * The project's delivery milestones in manual order with progress derived from linked tasks.
 * Query key starts `["projects", "delivery-milestones"]`.
 */
export const deliveryMilestonesQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["projects", "delivery-milestones", "list", projectId],
    queryFn: ({ signal }) =>
      request<DeliveryMilestoneList>(`/api/v1/projects/${projectId}/delivery-milestones`, { signal }),
  });

export const deliveryMilestoneQueryOptions = (milestoneId: number) =>
  queryOptions({
    queryKey: ["projects", "delivery-milestones", "detail", milestoneId],
    queryFn: ({ signal }) =>
      request<DeliveryMilestone>(`/api/v1/projects/delivery-milestones/${milestoneId}`, { signal }),
  });

/** A new delivery milestone is appended to the project, always 'open'. */
export const createDeliveryMilestone = (
  projectId: number,
  input: DeliveryMilestoneCreateInput,
): Promise<DeliveryMilestone> =>
  request<DeliveryMilestone>(`/api/v1/projects/${projectId}/delivery-milestones`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** A full replace: stale revision answers 409. */
export const updateDeliveryMilestone = (
  milestoneId: number,
  input: DeliveryMilestoneUpdateInput,
): Promise<DeliveryMilestone> =>
  request<DeliveryMilestone>(`/api/v1/projects/delivery-milestones/${milestoneId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** Deleting a milestone sets linked tasks' deliveryMilestoneId to null. */
export const deleteDeliveryMilestone = (milestoneId: number): Promise<void> =>
  request<void>(`/api/v1/projects/delivery-milestones/${milestoneId}`, { method: "DELETE" });

/** Reposition a milestone 1..n. */
export const moveDeliveryMilestone = (
  milestoneId: number,
  input: DeliveryMilestonePositionInput,
): Promise<DeliveryMilestone> =>
  request<DeliveryMilestone>(`/api/v1/projects/delivery-milestones/${milestoneId}/position`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** Toggle status between open and completed. */
export const setDeliveryMilestoneStatus = (
  milestoneId: number,
  input: DeliveryMilestoneStatusInput,
): Promise<DeliveryMilestone> =>
  request<DeliveryMilestone>(`/api/v1/projects/delivery-milestones/${milestoneId}/status`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
