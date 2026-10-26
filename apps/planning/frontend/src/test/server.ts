import type {
  PlanningAllocation,
  PlanningAllocationInput,
  PlanningAllocationWritten,
  PlanningWarning,
} from "../api/allocations";
import type { PlanningBoard, PlanningBoardRow, PlanningWeekFigures } from "../api/board";
import type { PlanningMe } from "../api/me";
import type { PlanningMeta } from "../api/meta";
import type { PlanningDateSpan, PlanningPerson } from "../api/people";
import type { PlanningProjectPlan } from "../api/plan";
import type { PlanningProject } from "../api/projects";
import type { PlanningSettings, PlanningSettingsInput } from "../api/settings";
import type { PlanningTimeline, PlanningTimelineFlag } from "../api/timeline";
import type { PlanningUnbooked, PlanningUnbookedPerson } from "../api/unbooked";
import { addDays, addWeeks, mondayOf, weeksBetween } from "../lib/weeks";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";
import {
  allocations as defaultAllocations,
  people as defaultPeople,
  projects as defaultProjects,
  settings as defaultSettings,
  MONDAY,
  TODAY,
} from "./fixtures";

/** Who is asking: `planning:access` alone, `planning:view`, or `planning:manage` (with view). */
export type Role = "access" | "view" | "manage";

export interface PlanningServer {
  /** Manage by default. */
  role?: Role;
  /** Fields of `/meta` over the role's answer — `workforceVisible: false`, say. */
  meta?: Partial<PlanningMeta>;
  people?: PlanningPerson[];
  projects?: PlanningProject[];
  allocations?: PlanningAllocation[];
  settings?: PlanningSettings;
  /** Each person's days away, by person id. */
  away?: Record<number, PlanningDateSpan[]>;
  /** The timeline's flags, any person's. */
  flags?: PlanningTimelineFlag[];
  /** The caller's person; null for nobody (`no_person`). Ada by default. */
  me?: number | null;
  unbooked?: PlanningUnbooked;
  /** What every allocation write warns of. */
  warnings?: PlanningWarning[];
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

export interface PlanningState {
  allocations: PlanningAllocation[];
  settings: PlanningSettings;
}

export type PlanningStub = StubbedFetch & { state: PlanningState };

/** A refusal in the module's problem shape (§4.7). */
export const planningProblem = (status: number, code: string | undefined, extra: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ status, title: extra.title ?? "Refused", code, ...extra }), {
    status,
    headers: { "Content-Type": "application/problem+json" },
  });

/** The hours a week every fixture person may work: 37.5, five days of 7.5. */
export const CAPACITY = 37.5;
const DAY = CAPACITY / 5;

const overlaps = (span: { from: string; to: string }, from: string, to: string) => span.from <= to && span.to >= from;
const covers = (row: PlanningAllocation, from: string, to: string) =>
  overlaps({ from: row.fromDate, to: row.toDate }, from, to);
const hoursOf = (row: PlanningAllocation) => (row.loadUnit === "percent" ? (CAPACITY * row.load) / 100 : row.load);
const round = (value: number) => Math.round(value * 100) / 100;

/**
 * An in-memory Planning API, answering what the pages ask for as the server
 * would for the role — `/meta` per role, the board's figures worked out from
 * the rows (a whole week's hours per row; a day away takes a day off the
 * available), the plan, the timeline (the self audience's confirmed rows and
 * nothing else), the unbooked list, the allocations with revisions and
 * warnings, My planning, the settings.
 */
export const stubPlanningApi = (server: PlanningServer = {}): PlanningStub => {
  const role = server.role ?? "manage";
  const people = server.people ?? defaultPeople();
  const projects = server.projects ?? defaultProjects();
  const away = server.away ?? {};
  const me = server.me === undefined ? 1 : server.me;
  const state: PlanningState = {
    allocations: server.allocations ?? defaultAllocations(),
    settings: server.settings ?? defaultSettings(),
  };
  let nextId = 900;

  const canView = role !== "access";
  const canManage = role === "manage";
  const meta = (): PlanningMeta => ({
    canView,
    canManage,
    hasPerson: me !== null,
    workforceVisible: true,
    loadUnits: ["percent", "hours_per_week"],
    ...(canManage ? { settings: state.settings } : {}),
    ...server.meta,
  });

  const projectOf = (id: number) => projects.find((one) => one.id === id);
  const withoutNote = (row: PlanningAllocation): PlanningAllocation => {
    const copy = { ...row };
    delete copy.note;
    return copy;
  };
  // The note is planning:manage's alone (S5).
  const shaped = (row: PlanningAllocation): PlanningAllocation => (canManage ? row : withoutNote(row));

  const figures = (monday: string, rows: PlanningAllocation[], personIds: number[], includeTentative: boolean) => {
    const sunday = addDays(monday, 6);
    const capacity = CAPACITY * personIds.length;
    const daysAway = personIds.reduce(
      (sum, id) =>
        sum +
        (away[id] ?? []).reduce((days, span) => {
          let count = 0;
          for (let day = monday; day <= addDays(monday, 4); day = addDays(day, 1))
            if (span.from <= day && span.to >= day) count += 1;
          return days + count;
        }, 0),
      0,
    );
    const inWeek = rows.filter((row) => covers(row, monday, sunday));
    const booked = inWeek.filter((row) => row.status === "confirmed").reduce((sum, row) => sum + hoursOf(row), 0);
    const tentative = includeTentative
      ? inWeek.filter((row) => row.status === "tentative").reduce((sum, row) => sum + hoursOf(row), 0)
      : 0;
    const available = capacity - daysAway * DAY;
    const answer: PlanningWeekFigures = {
      monday,
      capacity,
      available,
      booked: round(booked),
      tentative: round(tentative),
      free: round(available - booked),
      away: daysAway > 0,
    };
    if (capacity > 0) answer.loadPercent = round((booked / capacity) * 100);
    return answer;
  };

  const board = (search: URLSearchParams): PlanningBoard => {
    const from = mondayOf(search.get("from") ?? MONDAY);
    const to = search.get("to") ?? addDays(addWeeks(from, 12), -1);
    const includeTentative = search.get("includeTentative") !== "false";
    const includePlanned = search.get("includePlanned") !== "false";
    const departmentId = search.get("departmentId");
    const projectId = search.get("projectId");
    const mondays = weeksBetween(from, to);
    const windowRows = state.allocations.filter(
      (row) =>
        covers(row, from, to) &&
        (includePlanned || projectOf(row.project.id)?.status !== "planned") &&
        (includeTentative || row.status === "confirmed") &&
        (projectId === null || String(row.project.id) === projectId),
    );
    let shown = people.filter((person) => departmentId === null || String(person.department?.id) === departmentId);
    if (projectId !== null) shown = shown.filter((person) => windowRows.some((row) => row.person.id === person.id));
    const ids = new Set(shown.map((person) => person.id));
    const rowsShown = windowRows.filter((row) => ids.has(row.person.id));
    const rows: PlanningBoardRow[] =
      search.get("groupBy") === "project"
        ? projects
            .filter((project) => rowsShown.some((row) => row.project.id === project.id))
            .map((project) => {
              const own = rowsShown.filter((row) => row.project.id === project.id);
              const on = [...new Set(own.map((row) => row.person.id))];
              return { project, cells: mondays.map((monday) => figures(monday, own, on, includeTentative)) };
            })
        : shown.map((person) => ({
            person,
            cells: mondays.map((monday) =>
              figures(
                monday,
                rowsShown.filter((row) => row.person.id === person.id),
                [person.id],
                includeTentative,
              ),
            ),
          }));
    return { from, to, weeks: mondays.map((monday) => ({ monday })), rows, allocations: rowsShown.map(shaped) };
  };

  const plan = (projectId: number, search: URLSearchParams): PlanningProjectPlan | undefined => {
    const project = projectOf(projectId);
    if (!project) return undefined;
    const from = mondayOf(search.get("from") ?? MONDAY);
    const to = search.get("to") ?? addDays(addWeeks(from, 12), -1);
    const own = state.allocations.filter((row) => row.project.id === projectId && covers(row, from, to));
    return {
      project,
      allocations: own.map(shaped),
      weeks: weeksBetween(from, to).map((monday) => {
        const week = figures(monday, own, [], true);
        return { monday, booked: week.booked, tentative: week.tentative, demand: 0 };
      }),
    };
  };

  const timeline = (personId: number, search: URLSearchParams): PlanningTimeline | undefined => {
    const person = people.find((one) => one.id === personId);
    const self = !canView;
    if (!person || (self && personId !== me)) return undefined;
    const from = search.get("from") ?? TODAY;
    const to = search.get("to") ?? addWeeks(from, 26);
    const own = state.allocations.filter(
      (row) => row.person.id === personId && covers(row, from, to) && (!self || row.status === "confirmed"),
    );
    return {
      person,
      // The self audience reads its confirmed rows and nothing else (amendment 2).
      employed: self ? [] : person.employed,
      away: self ? [] : (away[personId] ?? []).filter((span) => overlaps(span, from, to)),
      allocations: own.map(shaped),
      flags: self ? [] : (server.flags ?? []).filter((flag) => own.some((row) => row.id === flag.allocationId)),
    };
  };

  const unbooked = (): PlanningUnbooked =>
    server.unbooked ?? {
      horizon: { from: TODAY, to: addWeeks(TODAY, state.settings.unbookedHorizonWeeks) },
      people: [
        { person: people[1], loadPercent: 0, freeHours: 225 },
        {
          person: people[2],
          loadPercent: 30.8,
          freeHours: 155.5,
          rollOff: state.allocations.find((row) => row.id === 503)?.toDate,
        } as PlanningUnbookedPerson,
      ],
    };

  const written = (allocation: PlanningAllocation, status = 200): Response =>
    jsonResponse(status, {
      allocation: shaped(allocation),
      warnings: server.warnings ?? [],
    } satisfies PlanningAllocationWritten);

  const fromInput = (
    input: PlanningAllocationInput,
    base: Partial<PlanningAllocation>,
  ): PlanningAllocation | Response => {
    const person = people.find((one) => one.id === input.personId);
    const project = projectOf(input.projectId);
    const errors: Record<string, string[]> = {};
    if (!person) errors.personId = ["Staff does not answer this person"];
    if (!project) errors.projectId = ["No such project"];
    if (Object.keys(errors).length > 0) return planningProblem(400, undefined, { title: "Invalid allocation", errors });
    if (project && (project.status === "completed" || project.status === "cancelled"))
      return planningProblem(409, "project_closed", { detail: "The project is closed." });
    const status = input.status ?? (project?.status === "planned" ? "tentative" : "confirmed");
    return {
      id: base.id ?? nextId++,
      revision: base.revision ?? 1,
      createdAt: base.createdAt ?? "2026-10-26T08:00:00Z",
      updatedAt: "2026-10-26T08:00:00Z",
      fromDate: input.fromDate,
      toDate: input.toDate,
      load: input.load,
      loadUnit: input.loadUnit,
      status,
      ...(status === "confirmed" ? { confirmedAt: "2026-10-26T08:00:00Z" } : {}),
      ...(input.title ? { title: input.title } : {}),
      ...(input.note ? { note: input.note } : {}),
      person: { id: person?.id ?? 0, displayName: person?.displayName ?? null, employeeNumber: person?.employeeNumber },
      project: { id: project?.id ?? 0, code: project?.code, name: project?.name, status: project?.status },
    };
  };

  const forbidden = () => jsonResponse(403, { error: { code: "forbidden", message: "Forbidden" } });

  const handle = (url: URL, method: string, init?: RequestInit): Response => {
    const path = url.pathname.replace(/^\/api\/v1\/planning/, "");
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    if (method !== "GET") {
      const override = server.write?.(method, path, body);
      if (override) return override;
    }
    const ifMatch = Number(new Headers(init?.headers).get("If-Match"));

    if (path === "/meta") return jsonResponse(200, meta());
    if (path === "/me") {
      const person = people.find((one) => one.id === me);
      if (!person)
        return planningProblem(404, "no_person", { title: "No person", detail: "Staff has no person linked." });
      return jsonResponse(200, {
        person,
        allocations: state.allocations
          .filter((row) => row.person.id === person.id && row.status === "confirmed" && row.toDate >= TODAY)
          .map(withoutNote),
      } satisfies PlanningMe);
    }
    const timelineMatch = /^\/people\/(\d+)\/timeline$/.exec(path);
    if (timelineMatch) {
      const answer = timeline(Number(timelineMatch[1]), url.searchParams);
      return answer ? jsonResponse(200, answer) : new Response(null, { status: 404 });
    }
    if (!canView) return forbidden();

    if (path === "/board") return jsonResponse(200, board(url.searchParams));
    if (path === "/people") {
      const q = url.searchParams.get("q")?.toLowerCase();
      return jsonResponse(200, {
        people: people.filter(
          (one) => !q || one.displayName.toLowerCase().includes(q) || one.employeeNumber.toLowerCase().includes(q),
        ),
      });
    }
    if (path === "/projects") {
      const statuses = (url.searchParams.get("status") ?? "planned,active,on-hold").split(",");
      const q = url.searchParams.get("q")?.toLowerCase();
      return jsonResponse(200, {
        projects: projects.filter(
          (one) =>
            statuses.includes(one.status) &&
            (!q || one.code.toLowerCase().includes(q) || one.name.toLowerCase().includes(q)),
        ),
      });
    }
    const planMatch = /^\/projects\/(\d+)\/plan$/.exec(path);
    if (planMatch) {
      const answer = plan(Number(planMatch[1]), url.searchParams);
      return answer ? jsonResponse(200, answer) : new Response(null, { status: 404 });
    }
    if (path === "/unbooked") return jsonResponse(200, unbooked());
    if (path === "/events") return jsonResponse(200, { events: [] });
    if (path === "/stats")
      return jsonResponse(200, { peoplePlannedThisWeek: 2, loadPercentThisWeek: 40, openRequests: 0, unbooked: 2 });
    if (path === "/stats/attention") return jsonResponse(200, []);

    if (path === "/allocations" && method === "GET")
      return jsonResponse(200, { allocations: state.allocations.map(shaped) });
    const allocationMatch = /^\/allocations\/(\d+)(\/confirm)?$/.exec(path);
    const existing = allocationMatch
      ? state.allocations.find((row) => row.id === Number(allocationMatch[1]))
      : undefined;
    if (allocationMatch && method === "GET")
      return existing ? jsonResponse(200, shaped(existing)) : new Response(null, { status: 404 });

    if (path === "/settings" && method === "GET") return canManage ? jsonResponse(200, state.settings) : forbidden();
    if (!canManage) return forbidden();

    if (path === "/settings" && method === "PUT") {
      if (ifMatch !== state.settings.revision)
        return planningProblem(409, "revision_conflict", { title: "Revision conflict" });
      state.settings = {
        ...state.settings,
        ...(body as PlanningSettingsInput),
        revision: state.settings.revision + 1,
      };
      return jsonResponse(200, state.settings);
    }
    if (path === "/allocations" && method === "POST") {
      const row = fromInput(body as PlanningAllocationInput, {});
      if (row instanceof Response) return row;
      state.allocations.push(row);
      return written(row, 201);
    }
    if (allocationMatch) {
      if (!existing) return new Response(null, { status: 404 });
      if (allocationMatch[2] && method === "POST") {
        const row: PlanningAllocation = {
          ...existing,
          status: "confirmed",
          confirmedAt: "2026-10-26T08:00:00Z",
          revision: existing.revision + (existing.status === "confirmed" ? 0 : 1),
        };
        state.allocations = state.allocations.map((one) => (one.id === row.id ? row : one));
        return written(row);
      }
      if (method === "PUT") {
        if (ifMatch !== existing.revision)
          return planningProblem(409, "revision_conflict", { title: "Revision conflict" });
        const row = fromInput(body as PlanningAllocationInput, { ...existing, revision: existing.revision + 1 });
        if (row instanceof Response) return row;
        state.allocations = state.allocations.map((one) => (one.id === row.id ? row : one));
        return written(row);
      }
      if (method === "DELETE") {
        state.allocations = state.allocations.filter((one) => one.id !== existing.id);
        return new Response(null, { status: 204 });
      }
    }
    return new Response(null, { status: 404 });
  };

  const stub = stubFetch((input: RequestInfo | URL, init?: RequestInit) =>
    Promise.resolve(handle(new URL(String(input), "http://localhost"), init?.method ?? "GET", init)),
  ) as PlanningStub;
  stub.state = state;
  return stub;
};
