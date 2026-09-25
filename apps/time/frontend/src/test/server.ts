import type { TimeApprovalGroup } from "../api/approvals";
import type { PaginatedResponse } from "../api/entries";
import type { TimePersonOverview } from "../api/people";
import type { MyProject, MyTaskOption, ProjectBillingLine } from "../api/projects";
import type { AssignableRateUser, PersonRate } from "../api/rates";
import type { TimeSettings } from "../api/settings";
import type { TimeProjectSummary } from "../api/stats";
import type { TimeWeek } from "../api/weeks";
import { jsonResponse } from "./api";
import { stubFetch } from "./fetch";
import { entry, kvemLines, myProjects, myTasks, WEEK, type WorkTypeWire, week } from "./fixtures";

/** A read the stub answers from a fixture, or a `Response` when the test wants a refusal. */
type Read<T> = T | Response;

export interface TimeServer {
  /** The caller's week, whichever Monday is asked for; a function is asked again on every read. */
  week?: TimeWeek | (() => TimeWeek);
  settings?: TimeSettings;
  projects?: MyProject[];
  lines?: Record<number, ProjectBillingLine[]>;
  /** Each project's work types as the projects API answers them; a project not named has none. */
  workTypes?: Record<number, WorkTypeWire[]>;
  tasks?: MyTaskOption[];
  /**
   * The approval queue, as its groups alone: the stub wraps them in a page.
   * A function is asked again on every read, so a test can change the queue
   * after a write the way the server would.
   */
  approvals?: Read<TimeApprovalGroup[]> | (() => TimeApprovalGroup[]);
  people?: Read<TimePersonOverview[]>;
  rates?: Read<PersonRate[]>;
  /** The directory search behind the rate form's person picker; the stub filters on the term. */
  assignableUsers?: Read<AssignableRateUser[]>;
  projectSummary?: Read<TimeProjectSummary>;
  /** Answers a write instead of the default success; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

/** The fixture, or the refusal the test put in its place; a Response is cloned so a refetch reads it again. */
const answer = <T>(read: Read<T> | undefined, fallback: T): Response =>
  read instanceof Response ? read.clone() : jsonResponse(200, read ?? fallback);

const page = <T>(data: T[]): PaginatedResponse<T> => ({
  data,
  pagination: {
    page: 1,
    pageSize: 25,
    totalCount: data.length,
    totalPages: 1,
    hasNextPage: false,
    hasPreviousPage: false,
  },
});

/**
 * The time and projects APIs as the pages read them, from fixtures. Writes
 * succeed unless `write` answers them otherwise; the returned mock records
 * every call, so a test reads back what was sent.
 */
export const stubTimeApi = (server: TimeServer = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    const path = url.pathname;
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    const currentWeek = (): TimeWeek => (typeof server.week === "function" ? server.week() : (server.week ?? week([])));

    if (method !== "GET") {
      const answered = server.write?.(method, path, body);
      if (answered) return Promise.resolve(answered);
      if (method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (path.endsWith("/submit") && path.startsWith("/api/v1/time/weeks/")) {
        return Promise.resolve(jsonResponse(200, currentWeek()));
      }
      if (path === "/api/v1/time/entries" && method === "POST") {
        return Promise.resolve(jsonResponse(201, entry({ ...body, id: 900, revision: 1 })));
      }
      if (path === "/api/v1/time/rates" && method === "POST") {
        return Promise.resolve(jsonResponse(201, { id: 99, displayName: "", ...body }));
      }
      if (path.startsWith("/api/v1/time/rates/")) return Promise.resolve(jsonResponse(200, { id: 99, ...body }));
      if (path === "/api/v1/time/settings") return Promise.resolve(jsonResponse(200, body));
      if (path.startsWith("/api/v1/time/entries/")) {
        return Promise.resolve(
          jsonResponse(
            200,
            (body?.ids ?? []).map((id: number) => entry({ id })),
          ),
        );
      }
      return Promise.resolve(jsonResponse(200, entry({ ...body })));
    }

    if (path.startsWith("/api/v1/time/weeks/")) {
      const weekStart = path.split("/").at(-1) ?? WEEK;
      return Promise.resolve(jsonResponse(200, { ...currentWeek(), weekStart }));
    }
    if (path === "/api/v1/time/settings") return Promise.resolve(jsonResponse(200, server.settings ?? {}));
    if (path === "/api/v1/time/approvals") {
      if (server.approvals instanceof Response) return Promise.resolve(server.approvals.clone());
      const groups = typeof server.approvals === "function" ? server.approvals() : (server.approvals ?? []);
      return Promise.resolve(jsonResponse(200, page(groups)));
    }
    if (path === "/api/v1/time/people") return Promise.resolve(answer(server.people, []));
    if (path === "/api/v1/time/rates") return Promise.resolve(answer(server.rates, []));
    if (path === "/api/v1/time/rates/assignable-users") {
      if (server.assignableUsers instanceof Response) return Promise.resolve(server.assignableUsers.clone());
      const term = (url.searchParams.get("search") ?? "").toLowerCase();
      const users = (server.assignableUsers ?? []).filter((user) => user.displayName.toLowerCase().includes(term));
      return Promise.resolve(jsonResponse(200, users));
    }
    if (/^\/api\/v1\/time\/projects\/\d+\/summary$/.test(path)) {
      return Promise.resolve(
        server.projectSummary instanceof Response
          ? server.projectSummary.clone()
          : server.projectSummary
            ? jsonResponse(200, server.projectSummary)
            : new Response(null, { status: 404 }),
      );
    }
    if (path === "/api/v1/projects") {
      return Promise.resolve(jsonResponse(200, { data: server.projects ?? myProjects, pagination: { page: 1 } }));
    }
    if (path === "/api/v1/projects/my-tasks") return Promise.resolve(jsonResponse(200, server.tasks ?? myTasks));
    const lines = /^\/api\/v1\/projects\/(\d+)\/billing-lines$/.exec(path);
    if (lines) {
      const all = server.lines ?? { 1001: kvemLines };
      return Promise.resolve(jsonResponse(200, all[Number(lines[1])] ?? []));
    }
    const types = /^\/api\/v1\/projects\/(\d+)\/work-types$/.exec(path);
    if (types) return Promise.resolve(jsonResponse(200, server.workTypes?.[Number(types[1])] ?? []));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
