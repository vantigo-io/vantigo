import { queryOptions } from "@tanstack/react-query";
import type { WorkType, WorkTypeInput } from "./projects";
import { request } from "./request";

export type { WorkType, WorkTypeInput } from "./projects";

/**
 * Every work type of the project, deactivated ones included, the active ones
 * first and each half by name — the order the server answers in. Under the
 * `["projects"]` root, so every write's blanket invalidation reaches it.
 */
export const workTypesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["projects", "detail", id, "work-types"],
    queryFn: ({ signal }) => request<WorkType[]>(`/api/v1/projects/${id}/work-types`, { signal }),
  });

const json = (method: string, input: WorkTypeInput): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(input),
});

/** A name the project already has, in any case, answers 409 (an `ApiConflictError`). */
export const createWorkType = (id: number, input: WorkTypeInput): Promise<WorkType> =>
  request<WorkType>(`/api/v1/projects/${id}/work-types`, json("POST", input));

/** There is no delete: `active: false` retires a type. A taken name answers 409 here too. */
export const updateWorkType = (id: number, workTypeId: number, input: WorkTypeInput): Promise<WorkType> =>
  request<WorkType>(`/api/v1/projects/${id}/work-types/${workTypeId}`, json("PUT", input));
