import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { ProjectPerson, ProjectRole, ProjectRoleAssignment } from "./projects";
import { request } from "./request";

export type { ProjectPerson, ProjectRole, ProjectRoleAssignment } from "./projects";

/** Managers first, then by display name — the order the API answers in. */
export const projectRolesQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["projects", "detail", id, "roles"],
    queryFn: ({ signal }) => request<ProjectRoleAssignment[]>(`/api/v1/projects/${id}/roles`, { signal }),
  });

/** At most 20 active users who are not already on the project. */
export const assignableUsersQueryOptions = (id: number, search: string) => {
  const term = search.trim();
  return queryOptions({
    queryKey: ["projects", "detail", id, "assignable-users", term],
    queryFn: ({ signal }) => {
      const query = term ? `?${new URLSearchParams({ search: term })}` : "";
      return request<ProjectPerson[]>(`/api/v1/projects/${id}/assignable-users${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });
};

/** Adds the user to the project, or changes the role they already have. */
export const setProjectRole = (id: number, userId: string, role: ProjectRole): Promise<ProjectRoleAssignment> =>
  request<ProjectRoleAssignment>(`/api/v1/projects/${id}/roles/${userId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ role }),
  });

export const removeProjectRole = (id: number, userId: string): Promise<void> =>
  request<void>(`/api/v1/projects/${id}/roles/${userId}`, { method: "DELETE" });
