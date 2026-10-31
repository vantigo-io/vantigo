import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

export type TaskDependency = Schemas["TaskDependencyResponse"];
export type TaskDependencyList = Schemas["TaskDependencyListResponse"];
export type TaskDependencyCreateInput = Schemas["TaskDependencyCreateRequest"];
export type TaskRescheduleDownstreamInput = Schemas["TaskRescheduleDownstreamRequest"];
export type TaskRescheduleDownstreamResult = Schemas["TaskRescheduleDownstreamResponse"];

/**
 * The project's task dependency links.
 * Query key starts `["projects", "dependencies"]`.
 */
export const projectDependenciesQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["projects", "dependencies", "list", projectId],
    queryFn: ({ signal }) => request<TaskDependencyList>(`/api/v1/projects/${projectId}/dependencies`, { signal }),
  });

/**
 * Creates a finish-to-start dependency link between two tasks of the project.
 */
export const createTaskDependency = (projectId: number, input: TaskDependencyCreateInput): Promise<TaskDependency> =>
  request<TaskDependency>(`/api/v1/projects/${projectId}/dependencies`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/**
 * Removes a task dependency link.
 */
export const deleteTaskDependency = (dependencyId: number): Promise<void> =>
  request<void>(`/api/v1/projects/dependencies/${dependencyId}`, { method: "DELETE" });

/**
 * Reschedules all downstream tasks reachable via dependencies by a delta in days.
 */
export const rescheduleDownstreamTasks = (
  taskId: number,
  input: TaskRescheduleDownstreamInput,
): Promise<TaskRescheduleDownstreamResult> =>
  request<TaskRescheduleDownstreamResult>(`/api/v1/projects/tasks/${taskId}/reschedule-downstream`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
