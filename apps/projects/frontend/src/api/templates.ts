import { queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import { request } from "./request";

type Schemas = components["schemas"];

export type ProjectTemplate = Schemas["ProjectTemplateResponse"];
export type ProjectTemplateList = Schemas["ProjectTemplateListResponse"];
export type ProjectTemplateMilestone = Schemas["ProjectTemplateMilestone"];
export type ProjectTemplateTask = Schemas["ProjectTemplateTask"];
export type ProjectTemplateSubtask = Schemas["ProjectTemplateSubtask"];
export type ProjectTemplateDependency = Schemas["ProjectTemplateDependency"];

export type ProjectTemplateCreateInput = Schemas["ProjectTemplateCreateRequest"];
export type ProjectTemplateUpdateInput = Schemas["ProjectTemplateUpdateRequest"];
export type ProjectApplyTemplateInput = Schemas["ProjectApplyTemplateRequest"];
export type ProjectApplyTemplateResult = Schemas["ProjectApplyTemplateResponse"];

/**
 * List of available reusable project templates.
 * Query key starts `["projects", "templates"]`.
 */
export const projectTemplatesQueryOptions = () =>
  queryOptions({
    queryKey: ["projects", "templates", "list"],
    queryFn: ({ signal }) => request<ProjectTemplateList>("/api/v1/projects/templates", { signal }),
  });

export const projectTemplateQueryOptions = (templateId: number) =>
  queryOptions({
    queryKey: ["projects", "templates", "detail", templateId],
    queryFn: ({ signal }) => request<ProjectTemplate>(`/api/v1/projects/templates/${templateId}`, { signal }),
  });

export const createProjectTemplate = (input: ProjectTemplateCreateInput): Promise<ProjectTemplate> =>
  request<ProjectTemplate>("/api/v1/projects/templates", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateProjectTemplate = (
  templateId: number,
  input: ProjectTemplateUpdateInput,
): Promise<ProjectTemplate> =>
  request<ProjectTemplate>(`/api/v1/projects/templates/${templateId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const deleteProjectTemplate = (templateId: number): Promise<void> =>
  request<void>(`/api/v1/projects/templates/${templateId}`, {
    method: "DELETE",
  });

export const applyProjectTemplate = (
  projectId: number,
  input: ProjectApplyTemplateInput,
): Promise<ProjectApplyTemplateResult> =>
  request<ProjectApplyTemplateResult>(`/api/v1/projects/${projectId}/apply-template`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
