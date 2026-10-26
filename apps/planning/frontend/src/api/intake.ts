import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { PLANNING_QUERY_KEY, request } from "./request";

type Schemas = components["schemas"];

export type PlanningIntake = Schemas["PlanningIntake"];
export type PlanningIntakeProject = Schemas["PlanningIntakeProject"];
export type PlanningIntakeRequest = Schemas["PlanningIntakeRequest"];
export type PlanningCoverage = Schemas["PlanningCoverage"];
export type PlanningIntakeFlag = PlanningIntakeRequest["flags"][number];

/** How a request of the pipeline is covered (S18). */
export const coverages: readonly PlanningCoverage[] = ["covered", "partly_covered", "uncovered"];

/** What an intake request may be flagged with: exactly one person covers it. */
export const intakeFlags: readonly PlanningIntakeFlag[] = ["key_person"];

/** The pipeline (S18): every planned project with its requests judged for coverage. `planning:view`'s. */
export const intakeQueryOptions = () =>
  queryOptions({
    queryKey: [PLANNING_QUERY_KEY, "intake"],
    queryFn: ({ signal }) => request<PlanningIntake>("/api/v1/planning/intake", { signal }),
  });
