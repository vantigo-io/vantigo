import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { json, PLANNING_QUERY_KEY, queryString, request, send } from "./request";

type Schemas = components["schemas"];

export type PlanningWish = Schemas["PlanningWish"];
export type PlanningWishInput = Schemas["PlanningWishInput"];
export type PlanningWishKind = Schemas["PlanningWishKind"];
export type PlanningWishList = Schemas["PlanningWishList"];

/** A wish is off a named project or for something new (S21); no free text. */
export const wishKinds: readonly PlanningWishKind[] = ["off", "new"];

/** The most open wishes a person may have (S21). */
export const MAX_OPEN_WISHES = 5;

export interface PlanningWishesFilter {
  personId?: number;
}

/** The open wishes, oldest first (S21). `planning:manage`'s. */
export const wishesQueryOptions = (filter: PlanningWishesFilter = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "wishes", filter],
    queryFn: ({ signal }) =>
      request<PlanningWishList>(`/api/v1/planning/wishes${queryString({ ...filter })}`, { signal }).then(
        (answer) => answer.wishes,
      ),
  });

/**
 * Files a wish of the caller's own (S21): off a project they have a
 * confirmed booking on (`wish_no_booking` otherwise), or for something new;
 * at most five open (`wish_limit`), one off a project (`wish_exists`).
 */
export const createMyWish = (input: PlanningWishInput): Promise<PlanningWish> =>
  send<PlanningWish>("/api/v1/planning/me/wishes", json("POST", input));

/** Withdraws a wish of the caller's own; it is deleted. */
export const withdrawMyWish = (id: number): Promise<void> =>
  send<void>(`/api/v1/planning/me/wishes/${id}`, { method: "DELETE" });

/** Stamps a wish seen by the planner; one already stamped is answered as it is. */
export const markWishSeen = (id: number): Promise<PlanningWish> =>
  send<PlanningWish>(`/api/v1/planning/wishes/${id}/seen`, { method: "POST" });

/** Resolves a wish: it is deleted, and the record keeps the kind, the project and the date. */
export const resolveWish = (id: number): Promise<void> =>
  send<void>(`/api/v1/planning/wishes/${id}/resolve`, { method: "POST" });
