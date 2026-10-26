import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type PlanningPerson = Schemas["PlanningPerson"];
export type PlanningPersonRef = Schemas["PlanningPersonRef"];
export type PlanningDateSpan = Schemas["PlanningDateSpan"];

export interface PlanningPeopleFilter {
  from?: string;
  to?: string;
  q?: string;
  departmentId?: number;
}

/** The people of a window as Staff answers them to the caller (S8), for the person picker. */
export const planningPeopleQueryOptions = (filter: PlanningPeopleFilter = {}) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "people", filter],
    queryFn: ({ signal }) =>
      request<Schemas["PlanningPeopleList"]>(`/api/v1/planning/people${queryString({ ...filter })}`, { signal }).then(
        (answer) => answer.people,
      ),
  });
