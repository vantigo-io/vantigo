import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, queryString, request } from "./request";

type Schemas = components["schemas"];

export type PlanningBoard = Schemas["PlanningBoard"];
export type PlanningBoardRow = Schemas["PlanningBoardRow"];
export type PlanningWeekFigures = Schemas["PlanningWeekFigures"];

export interface PlanningBoardQuery {
  from: string;
  to: string;
  groupBy?: "people" | "project";
  departmentId?: number;
  competenceId?: number;
  projectId?: number;
  includeTentative?: boolean;
  includePlanned?: boolean;
}

/** People (or projects) against weeks (S10): the figures of every week, and the allocations for the bars. */
export const boardQueryOptions = (query: PlanningBoardQuery) =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "board", query],
    queryFn: ({ signal }) =>
      request<PlanningBoard>(
        `/api/v1/planning/board${queryString({
          ...query,
          groupBy: query.groupBy === "project" ? "project" : undefined,
          includeTentative: query.includeTentative === false ? false : undefined,
          includePlanned: query.includePlanned === false ? false : undefined,
        })}`,
        { signal },
      ),
    // Paging the window keeps the old weeks on screen until the new ones arrive.
    placeholderData: keepPreviousData,
  });
