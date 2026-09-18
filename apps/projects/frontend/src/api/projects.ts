import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { components } from "../api-schema";
import type { BillingType, PricingMode } from "../lib/billing";
import type { ProjectRole } from "../lib/roles";
import type { ProjectStatus } from "../lib/status";
import { request } from "./request";

export type { BillingType, PricingMode } from "../lib/billing";
export type { ProjectRole } from "../lib/roles";
export type { ProjectStatus } from "../lib/status";
export { ApiValidationError, NotFoundError } from "./request";

type Schemas = components["schemas"];

/**
 * The contract declares the enumerations as plain strings (OpenAPI 3.0 without
 * enum members), so the generated rows are narrowed here to the exact strings
 * the backend accepts. Everything else is the generated shape as it stands.
 */
export type Project = Omit<Schemas["ProjectResponse"], "status" | "billingType"> & {
  status: ProjectStatus;
  billingType: BillingType;
};

export type ProjectSummary = Omit<Schemas["ProjectSummaryResponse"], "status" | "billingType"> & {
  status: ProjectStatus;
  billingType: BillingType;
};

export type ProjectInput = Omit<Schemas["ProjectCreateRequest"], "billingType"> & { billingType: BillingType };

/** An update carries every field plus the revision the project was read at. */
export type ProjectUpdateInput = ProjectInput & { revision: number };

export type ProjectCapabilities = Schemas["ProjectCapabilities"];
export type ProjectFinancials = Schemas["ProjectFinancials"];
export type ProjectPerson = Schemas["ProjectPersonSummary"];
export type TimelineEntry = Schemas["TimelineEntryResponse"];
export type ProjectStatusCounts = Schemas["GetProjectStatsResponse"];
export type ProjectCodeSuggestion = Schemas["ProjectCodeSuggestionResponse"];
export type PaginationMetadata = Schemas["PaginationMetadata"];

/** One user's role on one project, as `GET /projects/{id}/roles` answers it. */
export type ProjectRoleAssignment = Omit<Schemas["ProjectRoleResponse"], "role"> & { role: ProjectRole };

export type BillingLineListPrice = Schemas["BillingLineListPrice"];

export type BillingLinePricing = Omit<Schemas["BillingLinePricing"], "mode"> & { mode: PricingMode };

/** Pricing is absent — not null — when the caller may not see the project's amounts. */
export type BillingLine = Omit<Schemas["BillingLineResponse"], "pricing"> & { pricing?: BillingLinePricing };

export type BillingLineInput = Omit<Schemas["BillingLineRequest"], "pricingMode"> & { pricingMode: PricingMode };

export interface PaginatedResponse<T> {
  data: T[];
  pagination: PaginationMetadata;
}

export const PROJECTS_PAGE_SIZE = 25;
export const PROJECT_TIMELINE_PAGE_SIZE = 20;

/** The list page's URL search params, as the toolbar and pagination hold them. */
export interface ProjectListParams {
  page: number;
  search: string;
  /** The empty string is "any status"; the API is then asked for no status at all. */
  status: ProjectStatus | "";
  customerId?: number;
  /** Undefined lists both kinds; true only internal projects, false only customer ones. */
  internal?: boolean;
  mine: boolean;
}

const listQuery = (params: ProjectListParams): URLSearchParams => {
  const query = new URLSearchParams({ page: String(params.page), pageSize: String(PROJECTS_PAGE_SIZE) });
  const search = params.search.trim();
  if (search) query.set("search", search);
  if (params.status) query.set("status", params.status);
  if (params.customerId !== undefined) query.set("customerId", String(params.customerId));
  if (params.internal !== undefined) query.set("internal", String(params.internal));
  if (params.mine) query.set("mine", "true");
  return query;
};

export const projectsQueryOptions = (params: ProjectListParams) =>
  queryOptions({
    queryKey: ["projects", "list", params],
    queryFn: ({ signal }) =>
      request<PaginatedResponse<ProjectSummary>>(`/api/v1/projects?${listQuery(params)}`, { signal }),
    placeholderData: keepPreviousData,
  });

export const projectQueryOptions = (id: number) =>
  queryOptions({
    queryKey: ["projects", "detail", id],
    queryFn: ({ signal }) => request<Project>(`/api/v1/projects/${id}`, { signal }),
  });

/** How many of the projects the caller may see stand in each status. */
export const projectStatsQueryOptions = () =>
  queryOptions({
    queryKey: ["projects", "stats"],
    queryFn: ({ signal }) => request<ProjectStatusCounts>("/api/v1/projects/stats", { signal }),
  });

export const projectTimelineQueryOptions = (id: number, page: number) =>
  queryOptions({
    queryKey: ["projects", "detail", id, "timeline", page],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({ page: String(page), pageSize: String(PROJECT_TIMELINE_PAGE_SIZE) });
      return request<PaginatedResponse<TimelineEntry>>(`/api/v1/projects/${id}/timeline?${query}`, { signal });
    },
    placeholderData: keepPreviousData,
  });

export interface CodeSuggestionParams {
  /** Absent for an internal project; the suggestion is then derived from the name alone. */
  customerId?: number;
  name: string;
}

export const codeSuggestionQueryOptions = (params: CodeSuggestionParams) =>
  queryOptions({
    queryKey: ["projects", "code-suggestion", params],
    queryFn: ({ signal }) => {
      const query = new URLSearchParams();
      if (params.customerId !== undefined) query.set("customerId", String(params.customerId));
      query.set("name", params.name);
      return request<ProjectCodeSuggestion>(`/api/v1/projects/code-suggestion?${query}`, { signal });
    },
  });

export const createProject = (input: ProjectInput): Promise<Project> =>
  request<Project>("/api/v1/projects", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

/** A revision that has moved on answers 409; the form asks the user to reload. */
export const updateProject = (id: number, input: ProjectUpdateInput): Promise<Project> =>
  request<Project>(`/api/v1/projects/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const setProjectStatus = (id: number, status: ProjectStatus): Promise<Project> =>
  request<Project>(`/api/v1/projects/${id}/status`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ status }),
  });
