import { queryOptions } from "@tanstack/react-query";
import { type ApiError, request } from "./request";

/**
 * Cross-module reads. Time never imports the projects frontend; it calls the
 * projects HTTP API and declares here the little of each shape the row
 * picker and the entry form use, the way
 * `apps/projects/frontend/src/api/customers.ts` reads customers.
 */

/** A project the caller holds a role on. `billingType` decides whether the billable switch is offered. */
export interface MyProject {
  id: number;
  code: string;
  name: string;
  status: string;
  billingType: string;
}

/** An active billing line of a project, which time is logged against. */
export interface ProjectBillingLine {
  id: number;
  code: string;
  /** The project's code and the line's code joined with a hyphen ('KVEM1000-PM'). */
  trackableCode: string;
  productName?: string | null;
  active: boolean;
}

/** One of the caller's open tasks, with the project it belongs to. */
export interface MyTaskOption {
  id: number;
  title: string;
  projectId: number;
  projectCode: string;
  projectName: string;
}

interface ProjectListResponse {
  data: MyProject[];
}

/** One page is enough: nobody holds a role on more than a hundred projects they still log time on. */
const MY_PROJECTS_PAGE_SIZE = 100;

/** The billing type a project carries when none of its time is billed. */
export const NON_BILLABLE = "non-billable";

/** Projects accept time only while they are active. */
export const isLoggable = (project: MyProject): boolean => project.status === "active";

export const myProjectsQueryOptions = () =>
  queryOptions({
    queryKey: ["time", "options", "projects"],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({ mine: "true", pageSize: String(MY_PROJECTS_PAGE_SIZE) });
      const page = await request<ProjectListResponse>(`/api/v1/projects?${query}`, { signal });
      return page.data.map(
        ({ id, code, name, status, billingType }): MyProject => ({
          id,
          code,
          name,
          status,
          billingType,
        }),
      );
    },
  });

/**
 * The project's active lines. The projects API answers 409 when the products
 * module is off — the project then has no lines at all, which is what the
 * picker is told.
 */
export const projectBillingLinesQueryOptions = (projectId: number) =>
  queryOptions({
    queryKey: ["time", "options", "billing-lines", projectId],
    queryFn: async ({ signal }) => {
      try {
        const lines = await request<ProjectBillingLine[]>(`/api/v1/projects/${projectId}/billing-lines`, { signal });
        return lines
          .filter((line) => line.active)
          .map(
            ({ id, code, trackableCode, productName, active }): ProjectBillingLine => ({
              id,
              code,
              trackableCode,
              productName,
              active,
            }),
          );
      } catch (error) {
        if ((error as ApiError).status === 409) return [];
        throw error;
      }
    },
  });

/** The caller's open tasks across every project they can see. */
export const myOpenTasksQueryOptions = () =>
  queryOptions({
    queryKey: ["time", "options", "my-tasks"],
    queryFn: async ({ signal }) => {
      const tasks = await request<MyTaskOption[]>("/api/v1/projects/my-tasks", { signal });
      return tasks.map(
        ({ id, title, projectId, projectCode, projectName }): MyTaskOption => ({
          id,
          title,
          projectId,
          projectCode,
          projectName,
        }),
      );
    },
  });
