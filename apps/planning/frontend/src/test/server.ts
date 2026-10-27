import type {
  PlanningAllocation,
  PlanningAllocationInput,
  PlanningAllocationWritten,
  PlanningWarning,
} from "../api/allocations";
import type { PlanningBoard, PlanningBoardRow, PlanningWeekFigures } from "../api/board";
import type { PlanningCompetence } from "../api/competences";
import type { PlanningIntake } from "../api/intake";
import type { PlanningMe } from "../api/me";
import type { PlanningMeta } from "../api/meta";
import type { PlanningDateSpan, PlanningPerson } from "../api/people";
import type { PlanningProjectPlan } from "../api/plan";
import type { PlanningProject } from "../api/projects";
import type {
  PlanningCandidate,
  PlanningFillInput,
  PlanningRequest,
  PlanningRequestFilled,
  PlanningRequestInput,
  PlanningRequestWritten,
} from "../api/requests";
import type { PlanningSettings, PlanningSettingsInput } from "../api/settings";
import type { PlanningTerms, PlanningTermsInput } from "../api/terms";
import type { PlanningTimeline, PlanningTimelineFlag } from "../api/timeline";
import type { PlanningUnbooked, PlanningUnbookedPerson } from "../api/unbooked";
import type { PlanningWish, PlanningWishInput } from "../api/wishes";
import { addDays, addWeeks, mondayOf, weeksBetween } from "../lib/weeks";
import { jsonResponse } from "./api";
import { type StubbedFetch, stubFetch } from "./fetch";
import {
  allocations as defaultAllocations,
  candidates as defaultCandidates,
  competences as defaultCompetences,
  people as defaultPeople,
  projects as defaultProjects,
  requests as defaultRequests,
  settings as defaultSettings,
  wishes as defaultWishes,
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
  /** The calendar's busy hours and days in every week, by person id; a person left out has no answer. */
  busy?: Record<number, { hours: number; days: number }>;
  /** The timeline's flags, any person's. */
  flags?: PlanningTimelineFlag[];
  /** The caller's person; null for nobody (`no_person`). Ada by default. */
  me?: number | null;
  unbooked?: PlanningUnbooked;
  /** What every allocation write warns of. */
  warnings?: PlanningWarning[];
  requests?: PlanningRequest[];
  competences?: PlanningCompetence[];
  /** Every request's candidates, the partial ones (with `missing`) left out unless asked for. */
  candidates?: PlanningCandidate[];
  /** What every request write warns of. */
  requestWarnings?: PlanningWarning[];
  intake?: PlanningIntake;
  /** The open wishes, anyone's. */
  wishes?: PlanningWish[];
  /** The customer directory's answer to a search (`GET /api/v1/customers`); "forbidden" for a 403. */
  customers?: { id: number; name: string }[] | "forbidden";
  /** Answers a write instead of the fake's own; undefined falls through to it. */
  write?: (method: string, path: string, body: unknown) => Response | undefined;
}

export interface PlanningState {
  allocations: PlanningAllocation[];
  settings: PlanningSettings;
  requests: PlanningRequest[];
  wishes: PlanningWish[];
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

/** The customer directory the fake answers: the fixture projects' customers and one more. */
const DIRECTORY = [
  { id: 7, name: "Fjord AS" },
  { id: 8, name: "Bergen Havn" },
  { id: 9, name: "Nordlys Energi" },
];

/** A date plus a count of months, the day kept (or the month's last). */
const addMonths = (date: string, months: number) => {
  const [y, m, d] = date.split("-").map(Number);
  const last = new Date(Date.UTC(y, m - 1 + months + 1, 0)).getUTCDate();
  return new Date(Date.UTC(y, m - 1 + months, Math.min(d, last))).toISOString().slice(0, 10);
};

/** The derived dates (S15): the latest of the end, the minimum and the notice's end; the handover before it. */
const derived = (row: PlanningAllocation): PlanningAllocation => {
  const terms = row.terms;
  let release = row.toDate;
  if (terms?.minimumUntil && terms.minimumUntil > release) release = terms.minimumUntil;
  if (terms?.noticeGivenOn && terms.noticeCount && terms.noticeUnit) {
    const end =
      terms.noticeUnit === "months"
        ? addMonths(terms.noticeGivenOn, terms.noticeCount)
        : addDays(terms.noticeGivenOn, terms.noticeCount * (terms.noticeUnit === "weeks" ? 7 : 1));
    if (end > release) release = end;
  }
  const answer: PlanningAllocation = { ...row, earliestRelease: release };
  delete answer.handoverStart;
  if (terms?.handoverDays !== undefined) answer.handoverStart = addDays(release, -terms.handoverDays);
  return answer;
};

/**
 * An in-memory Planning API, answering what the pages ask for as the server
 * would for the role — `/meta` per role, the board's figures worked out from
 * the rows (a whole week's hours per row; a day away takes a day off the
 * available), the plan, the timeline (the self audience's confirmed rows and
 * nothing else), the unbooked list, the allocations with revisions and
 * warnings, My planning, the settings; the requests with their writes, fill
 * and withdrawal, their candidates, the intake and the competence catalogue;
 * the terms with their own revision and the derived dates, the wishes, and
 * the customer directory's search.
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
    requests: server.requests ?? defaultRequests(),
    wishes: server.wishes ?? defaultWishes(),
  };
  const competences = server.competences ?? defaultCompetences();
  const candidateList = server.candidates ?? defaultCandidates();
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
  /** The note and the terms are planning:manage's alone (S5, S14); the derived dates are everyone's. */
  const withoutNote = (row: PlanningAllocation): PlanningAllocation => {
    const copy = { ...row };
    delete copy.note;
    delete copy.terms;
    return copy;
  };
  const shaped = (row: PlanningAllocation): PlanningAllocation => (canManage ? row : withoutNote(row));
  /** The caller's own open wishes, or one person's, oldest first. */
  const wishesOf = (personId: number) =>
    state.wishes
      .filter((one) => one.person.id === personId)
      .sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id - b.id);
  const shapedRequest = (row: PlanningRequest): PlanningRequest => {
    if (canManage) return row;
    const copy = { ...row };
    delete copy.note;
    return copy;
  };
  /** An open request's hours in a week: its hours a week × its weekdays inside the week / 5 (S11). */
  const demandOf = (row: PlanningRequest, monday: string) => {
    let days = 0;
    for (let day = monday; day <= addDays(monday, 4); day = addDays(day, 1))
      if (row.fromDate <= day && row.toDate >= day) days += 1;
    return (row.hoursPerWeek * days) / 5;
  };

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
    const busyOf = personIds.filter((id) => server.busy?.[id]);
    if (busyOf.length > 0) {
      answer.busyHours = busyOf.reduce((sum, id) => sum + (server.busy?.[id]?.hours ?? 0), 0);
      answer.busyDays = busyOf.reduce((sum, id) => sum + (server.busy?.[id]?.days ?? 0), 0);
    }
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
    const requests = state.requests.filter(
      (row) =>
        row.project.id === projectId &&
        row.status !== "withdrawn" &&
        overlaps({ from: row.fromDate, to: row.toDate }, from, to),
    );
    const open = requests.filter((row) => row.status === "open");
    return {
      project,
      allocations: own.map(shaped),
      requests: requests.map(shapedRequest),
      weeks: weeksBetween(from, to).map((monday) => {
        const week = figures(monday, own, [], true);
        const demand = round(open.reduce((sum, row) => sum + demandOf(row, monday), 0));
        return { monday, booked: week.booked, tentative: week.tentative, demand };
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
      // To planning:manage and to the person themself; empty to view alone (S21).
      wishes: canManage || self ? wishesOf(personId) : [],
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
    return derived({
      id: base.id ?? nextId++,
      revision: base.revision ?? 1,
      createdAt: base.createdAt ?? "2026-10-26T08:00:00Z",
      updatedAt: "2026-10-26T08:00:00Z",
      fromDate: input.fromDate,
      toDate: input.toDate,
      earliestRelease: input.toDate,
      load: input.load,
      loadUnit: input.loadUnit,
      status,
      ...(status === "confirmed" ? { confirmedAt: "2026-10-26T08:00:00Z" } : {}),
      ...(input.title ? { title: input.title } : {}),
      ...(input.note ? { note: input.note } : {}),
      ...(base.terms ? { terms: base.terms } : {}),
      person: { id: person?.id ?? 0, displayName: person?.displayName ?? null, employeeNumber: person?.employeeNumber },
      project: { id: project?.id ?? 0, code: project?.code, name: project?.name, status: project?.status },
    });
  };

  /** A wish of the caller's own (S21): the project's booking, five open at most, one off a project. */
  const fileWish = (input: PlanningWishInput): Response => {
    const person = people.find((one) => one.id === me);
    if (!person) return planningProblem(404, "no_person", { title: "No person" });
    const errors: Record<string, string[]> = {};
    if (input.kind === "off" && input.projectId === undefined) errors.projectId = ["Name the project"];
    if (input.kind === "new" && input.projectId !== undefined) errors.projectId = ["A new wish names no project"];
    if (!input.fromDate || input.fromDate < TODAY) errors.fromDate = ["From today on"];
    if (Object.keys(errors).length > 0) return planningProblem(400, undefined, { title: "Invalid wish", errors });
    const project = input.projectId === undefined ? undefined : projectOf(input.projectId);
    if (
      input.kind === "off" &&
      !state.allocations.some(
        (row) =>
          row.person.id === person.id &&
          row.project.id === input.projectId &&
          row.status === "confirmed" &&
          row.toDate >= TODAY,
      )
    )
      return planningProblem(409, "wish_no_booking", { title: "No booking" });
    const open = wishesOf(person.id);
    if (open.length >= 5) return planningProblem(409, "wish_limit", { title: "Too many wishes" });
    if (input.kind === "off" && open.some((one) => one.kind === "off" && one.project?.id === input.projectId))
      return planningProblem(409, "wish_exists", { title: "Wish exists" });
    const filed: PlanningWish = {
      id: nextId++,
      person: { id: person.id, displayName: person.displayName, employeeNumber: person.employeeNumber },
      kind: input.kind,
      ...(project
        ? { project: { id: project.id, code: project.code, name: project.name, status: project.status } }
        : input.projectId === undefined
          ? {}
          : { project: { id: input.projectId } }),
      fromDate: input.fromDate,
      createdAt: "2026-10-26T08:00:00Z",
    };
    state.wishes.push(filed);
    return jsonResponse(201, filed);
  };

  /** Terms set whole (S14) at the terms' own revision, the customers named from the directory. */
  const setTerms = (row: PlanningAllocation, input: PlanningTermsInput, ifMatch: string | null): Response => {
    const stored = row.terms;
    if (stored ? ifMatch !== String(stored.revision) : ifMatch !== null)
      return planningProblem(409, "revision_conflict", { title: "Revision conflict" });
    const project = projectOf(row.project.id);
    if (project && (project.status === "completed" || project.status === "cancelled"))
      return planningProblem(409, "project_closed", { title: "Project closed", detail: "The project is closed." });
    const ids = input.nonCompeteCustomerIds ?? [];
    const errors: Record<string, string[]> = {};
    if (ids.some((id) => !DIRECTORY.some((one) => one.id === id))) errors.nonCompeteCustomerIds = ["No such customer"];
    if (ids.length > 0 && !input.nonCompeteUntil) errors.nonCompeteUntil = ["Give the day the non-compete lasts until"];
    if (Object.keys(errors).length > 0) return planningProblem(400, undefined, { title: "Invalid allocation", errors });
    const fields: PlanningTermsInput = { ...input };
    delete fields.nonCompeteCustomerIds;
    const terms: PlanningTerms = {
      ...fields,
      replacementNeedsApproval: input.replacementNeedsApproval ?? false,
      nonCompeteCustomers: ids.map((id) => ({ id, name: DIRECTORY.find((one) => one.id === id)?.name })),
      revision: (stored?.revision ?? 0) + 1,
      createdAt: stored?.createdAt ?? "2026-10-26T08:00:00Z",
      updatedAt: "2026-10-26T08:00:00Z",
    };
    const answer = derived({ ...row, terms });
    state.allocations = state.allocations.map((one) => (one.id === row.id ? answer : one));
    return jsonResponse(200, answer);
  };

  const covering = () => candidateList.filter((one) => one.covers).length;
  const intake = (): PlanningIntake =>
    server.intake ?? {
      projects: projects
        .filter((project) => project.status === "planned")
        .map((project) => {
          const own = state.requests.filter((row) => row.project.id === project.id && row.status !== "withdrawn");
          const rows = state.allocations.filter((row) => row.project.id === project.id);
          const sum = (status: string) =>
            round(rows.filter((row) => row.status === status).reduce((total, row) => total + hoursOf(row), 0));
          return {
            project,
            requestedHoursPerWeek: own
              .filter((row) => row.status === "open")
              .reduce((t, row) => t + row.hoursPerWeek, 0),
            tentativeHoursPerWeek: sum("tentative"),
            confirmedHoursPerWeek: sum("confirmed"),
            requests: own.map((row) => {
              const count = covering();
              return {
                request: shapedRequest(row),
                coverage:
                  row.status === "filled" || count > 0
                    ? "covered"
                    : candidateList.length > 0
                      ? "partly_covered"
                      : "uncovered",
                candidates: count,
                flags: row.status === "open" && count === 1 ? ["key_person"] : [],
              };
            }),
          };
        }),
    };

  const requestFromInput = (
    input: PlanningRequestInput,
    base: Partial<PlanningRequest>,
  ): PlanningRequest | Response => {
    const project = projectOf(input.projectId);
    if (!project)
      return planningProblem(400, undefined, { title: "Invalid request", errors: { projectId: ["No such project"] } });
    if (project.status === "completed" || project.status === "cancelled")
      return planningProblem(409, "project_closed", { title: "Project closed", detail: "The project is closed." });
    return {
      id: base.id ?? nextId++,
      revision: base.revision ?? 1,
      status: base.status ?? "open",
      createdAt: base.createdAt ?? "2026-10-26T08:00:00Z",
      updatedAt: "2026-10-26T08:00:00Z",
      project: { id: project.id, code: project.code, name: project.name, status: project.status },
      title: input.title,
      fromDate: input.fromDate,
      toDate: input.toDate,
      hoursPerWeek: input.hoursPerWeek,
      ...(input.note ? { note: input.note } : {}),
      requirements: (input.requirements ?? []).map((requirement) => {
        const competence = competences.find((one) => one.id === requirement.competenceId);
        return {
          competenceId: requirement.competenceId,
          ...(competence
            ? {
                key: competence.key,
                nameEn: competence.nameEn,
                nameNb: competence.nameNb,
                kind: competence.kind,
                hasLevel: competence.hasLevel,
              }
            : {}),
          ...(requirement.minLevel === undefined ? {} : { minLevel: requirement.minLevel }),
        };
      }),
    };
  };
  const notOpen = () =>
    planningProblem(409, "request_not_open", { title: "Request not open", detail: "The request is not open." });

  const forbidden = () => jsonResponse(403, { error: { code: "forbidden", message: "Forbidden" } });

  const handle = (url: URL, method: string, init?: RequestInit): Response => {
    const path = url.pathname.replace(/^\/api\/v1\/planning/, "");
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    if (method !== "GET") {
      const override = server.write?.(method, path, body);
      if (override) return override;
    }
    const ifMatchHeader = new Headers(init?.headers).get("If-Match");
    const ifMatch = Number(ifMatchHeader);

    // The customer directory, another module's API: the non-compete's picker searches it.
    if (url.pathname === "/api/v1/customers") {
      if (server.customers === "forbidden") return forbidden();
      const term = url.searchParams.get("search")?.toLowerCase();
      const data = (server.customers ?? DIRECTORY).filter((one) => !term || one.name.toLowerCase().includes(term));
      return jsonResponse(200, { data, pagination: { page: 1, pageSize: 20, total: data.length } });
    }

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
        wishes: wishesOf(person.id),
      } satisfies PlanningMe);
    }
    if (path === "/me/wishes" && method === "POST") return fileWish(body as PlanningWishInput);
    const myWishMatch = /^\/me\/wishes\/(\d+)$/.exec(path);
    if (myWishMatch && method === "DELETE") {
      const own = state.wishes.find((one) => one.id === Number(myWishMatch[1]) && one.person.id === me);
      if (!own) return planningProblem(404, undefined, { title: "No such wish" });
      state.wishes = state.wishes.filter((one) => one.id !== own.id);
      return new Response(null, { status: 204 });
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
    if (path === "/competences") return jsonResponse(200, { competences });
    if (path === "/intake") return jsonResponse(200, intake());
    if (path === "/requests" && method === "GET") {
      const status = url.searchParams.get("status");
      const projectId = url.searchParams.get("projectId");
      return jsonResponse(200, {
        requests: state.requests
          .filter(
            (row) =>
              (status === null || row.status === status) &&
              (projectId === null || String(row.project.id) === projectId),
          )
          .map(shapedRequest),
      });
    }
    const requestMatch = /^\/requests\/(\d+)(\/candidates|\/fill|\/withdraw)?$/.exec(path);
    const existingRequest = requestMatch ? state.requests.find((row) => row.id === Number(requestMatch[1])) : undefined;
    if (requestMatch && !requestMatch[2] && method === "GET")
      return existingRequest ? jsonResponse(200, shapedRequest(existingRequest)) : new Response(null, { status: 404 });

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

    if (path === "/wishes" && method === "GET") {
      const personId = url.searchParams.get("personId");
      return jsonResponse(200, {
        wishes: state.wishes
          .filter((one) => personId === null || String(one.person.id) === personId)
          .sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id - b.id),
      });
    }
    const wishMatch = /^\/wishes\/(\d+)\/(seen|resolve)$/.exec(path);
    if (wishMatch && method === "POST") {
      const found = state.wishes.find((one) => one.id === Number(wishMatch[1]));
      if (!found) return new Response(null, { status: 404 });
      if (wishMatch[2] === "resolve") {
        state.wishes = state.wishes.filter((one) => one.id !== found.id);
        return new Response(null, { status: 204 });
      }
      const stamped: PlanningWish = { ...found, seenAt: found.seenAt ?? "2026-10-26T09:00:00Z" };
      state.wishes = state.wishes.map((one) => (one.id === found.id ? stamped : one));
      return jsonResponse(200, stamped);
    }
    const termsMatch = /^\/allocations\/(\d+)\/terms$/.exec(path);
    if (termsMatch) {
      const row = state.allocations.find((one) => one.id === Number(termsMatch[1]));
      if (!row) return new Response(null, { status: 404 });
      if (method === "PUT") return setTerms(row, body as PlanningTermsInput, ifMatchHeader);
      if (method === "DELETE") {
        const cleared = { ...row };
        delete cleared.terms;
        state.allocations = state.allocations.map((one) => (one.id === row.id ? derived(cleared) : one));
        return new Response(null, { status: 204 });
      }
    }

    if (requestMatch?.[2] === "/candidates" && method === "GET") {
      if (!existingRequest) return new Response(null, { status: 404 });
      const includePartial = url.searchParams.get("includePartial") === "true";
      return jsonResponse(200, {
        includePartial,
        candidates: candidateList.filter((one) => includePartial || one.missing.length === 0),
      });
    }
    if (path === "/requests" && method === "POST") {
      const row = requestFromInput(body as PlanningRequestInput, {});
      if (row instanceof Response) return row;
      state.requests.push(row);
      return jsonResponse(201, {
        request: row,
        warnings: server.requestWarnings ?? [],
      } satisfies PlanningRequestWritten);
    }
    if (requestMatch) {
      if (!existingRequest) return new Response(null, { status: 404 });
      if (requestMatch[2] === "/withdraw" && method === "POST") {
        if (existingRequest.status !== "open") return notOpen();
        const row: PlanningRequest = {
          ...existingRequest,
          status: "withdrawn",
          revision: existingRequest.revision + 1,
        };
        state.requests = state.requests.map((one) => (one.id === row.id ? row : one));
        return jsonResponse(200, row);
      }
      if (requestMatch[2] === "/fill" && method === "POST") {
        if (existingRequest.status !== "open") return notOpen();
        const input = body as PlanningFillInput;
        const allocation = fromInput(
          {
            personId: input.personId,
            projectId: existingRequest.project.id,
            fromDate: existingRequest.fromDate,
            toDate: existingRequest.toDate,
            load: existingRequest.hoursPerWeek,
            loadUnit: "hours_per_week",
            title: existingRequest.title,
            ...(input.status ? { status: input.status } : {}),
          },
          {},
        );
        if (allocation instanceof Response) return allocation;
        allocation.requestId = existingRequest.id;
        state.allocations.push(allocation);
        const row: PlanningRequest = {
          ...existingRequest,
          status: "filled",
          revision: existingRequest.revision + 1,
          filledBy: { allocationId: allocation.id, person: allocation.person },
        };
        state.requests = state.requests.map((one) => (one.id === row.id ? row : one));
        return jsonResponse(201, {
          request: row,
          allocation,
          warnings: server.warnings ?? [],
        } satisfies PlanningRequestFilled);
      }
      if (!requestMatch[2] && method === "PUT") {
        if (ifMatch !== existingRequest.revision)
          return planningProblem(409, "revision_conflict", { title: "Revision conflict" });
        if (existingRequest.status !== "open") return notOpen();
        const row = requestFromInput(body as PlanningRequestInput, {
          ...existingRequest,
          revision: existingRequest.revision + 1,
        });
        if (row instanceof Response) return row;
        state.requests = state.requests.map((one) => (one.id === row.id ? row : one));
        return jsonResponse(200, {
          request: row,
          warnings: server.requestWarnings ?? [],
        } satisfies PlanningRequestWritten);
      }
    }
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
        // A request is filled exactly while a row names it (S16).
        state.requests = state.requests.map((one) =>
          one.id === existing.requestId ? { ...one, status: "open", filledBy: undefined } : one,
        );
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
