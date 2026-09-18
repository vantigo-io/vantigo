import type { MyProject, MyTaskOption, ProjectBillingLine } from "../api/projects";
import type { TimeSettings } from "../api/settings";
import type { TimeWeek } from "../api/weeks";
import { jsonResponse } from "./api";
import { stubFetch } from "./fetch";
import { entry, kvemLines, myProjects, myTasks, WEEK, week } from "./fixtures";

export interface TimeServer {
  /** The caller's week, whichever Monday is asked for; a function is asked again on every read. */
  week?: TimeWeek | (() => TimeWeek);
  settings?: TimeSettings;
  projects?: MyProject[];
  lines?: Record<number, ProjectBillingLine[]>;
  tasks?: MyTaskOption[];
  /** Answers a write instead of the default success; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

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
      const answer = server.write?.(method, path, body);
      if (answer) return Promise.resolve(answer);
      if (method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (path.endsWith("/submit") && path.startsWith("/api/v1/time/weeks/")) {
        return Promise.resolve(jsonResponse(200, currentWeek()));
      }
      if (path === "/api/v1/time/entries" && method === "POST") {
        return Promise.resolve(jsonResponse(201, entry({ ...body, id: 900, revision: 1 })));
      }
      return Promise.resolve(jsonResponse(200, entry({ ...body })));
    }

    if (path.startsWith("/api/v1/time/weeks/")) {
      const weekStart = path.split("/").at(-1) ?? WEEK;
      return Promise.resolve(jsonResponse(200, { ...currentWeek(), weekStart }));
    }
    if (path === "/api/v1/time/settings") return Promise.resolve(jsonResponse(200, server.settings ?? {}));
    if (path === "/api/v1/projects") {
      return Promise.resolve(jsonResponse(200, { data: server.projects ?? myProjects, pagination: { page: 1 } }));
    }
    if (path === "/api/v1/projects/my-tasks") return Promise.resolve(jsonResponse(200, server.tasks ?? myTasks));
    const lines = /^\/api\/v1\/projects\/(\d+)\/billing-lines$/.exec(path);
    if (lines) {
      const all = server.lines ?? { 1001: kvemLines };
      return Promise.resolve(jsonResponse(200, all[Number(lines[1])] ?? []));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });
