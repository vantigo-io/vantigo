import type { PeopleStatusFilter } from "../api/people";
import { isIsoDate } from "./dates";

/**
 * How long a picker waits after the last keystroke before it searches. The
 * tests run with none, so what they wait on is the stub's answer, not a timer.
 */
export const SEARCH_DEBOUNCE_MS = import.meta.env.MODE === "test" ? 0 : 250;

/**
 * People's URL search params. Every filter lives in the URL so the list
 * survives a refresh and travels in a pasted link; the page reads them with
 * `useSearch` and writes them with `navigate`.
 */
export interface PeopleSearch {
  q?: string;
  unitId?: number;
  departmentId?: number;
  locationId?: number;
  /** `staff:manage`'s alone; left out, the server's `current`. */
  status?: PeopleStatusFilter;
  page: number;
  /**
   * A form to open on arrival — the seam the host's Spotlight "New person"
   * action lands on, as every other app's create action does.
   */
  create?: "person";
}

const positiveId = (value: unknown): number | undefined => {
  const id = Number(value);
  return Number.isInteger(id) && id > 0 ? id : undefined;
};

const statuses: readonly PeopleStatusFilter[] = ["current", "ended", "all"];

/**
 * The host route's `validateSearch` for People, exported so the route and
 * this package's tests validate the URL the same way. Anything a hand-edited
 * link invents falls back to "no filter" rather than reaching the API as a
 * value it would refuse with a 400.
 */
export const validatePeopleSearch = (search: Record<string, unknown>): PeopleSearch => ({
  q: typeof search.q === "string" && search.q.trim() ? search.q : undefined,
  unitId: positiveId(search.unitId),
  departmentId: positiveId(search.departmentId),
  locationId: positiveId(search.locationId),
  status: statuses.includes(search.status as PeopleStatusFilter) ? (search.status as PeopleStatusFilter) : undefined,
  page: Math.max(1, Number(search.page) || 1),
  create: search.create === "person" ? "person" : undefined,
});

/**
 * The tabs of the person page and My page (design §7), with delivery C's
 * Account — the tab the host's dashboard links an account item to — and
 * phase 2's Competence, which its competence items open.
 */
export const personTabs = [
  "profile",
  "employments",
  "absence",
  "competence",
  "history",
  "documents",
  "account",
] as const;
export type PersonTab = (typeof personTabs)[number];

/** The person page's and My page's URL search: the tab, so a refresh or a pasted link opens the same view. */
export interface PersonSearch {
  tab?: PersonTab;
}

/** The host route's `validateSearch` for the person page and My page; an unknown tab is the profile. */
export const validatePersonSearch = (search: Record<string, unknown>): PersonSearch => ({
  tab: personTabs.includes(search.tab as PersonTab) && search.tab !== "profile" ? (search.tab as PersonTab) : undefined,
});

/**
 * My page's tabs: a person's, then my absence and — for a caller who manages
 * anyone — My team (plan reading 6: a section of My page, since the host's
 * sidebar is gated by permission and cannot know who manages anyone).
 */
export const myPageTabs = [...personTabs, "team"] as const;
export type MyPageTab = (typeof myPageTabs)[number];

/**
 * My page's URL search: the tab, and the form to open on arrival (the host's
 * spotlight actions) — the request sheet, or Add competence.
 */
export interface MyPageSearch {
  tab?: MyPageTab;
  create?: "absence" | "competence";
}

/** The host route's `validateSearch` for My page; an unknown tab is the profile. */
export const validateMyPageSearch = (search: Record<string, unknown>): MyPageSearch => ({
  tab: myPageTabs.includes(search.tab as MyPageTab) && search.tab !== "profile" ? (search.tab as MyPageTab) : undefined,
  create: search.create === "absence" || search.create === "competence" ? search.create : undefined,
});

/** The Absence page's tabs (design §7): the calendar, and the inbox of what the caller decides. */
export const absenceTabs = ["calendar", "approvals"] as const;
export type AbsenceTab = (typeof absenceTabs)[number];

/** The Absence page's URL search: the tab, the month shown (`YYYY-MM`) and the department it is narrowed to. */
export interface AbsenceSearch {
  tab?: AbsenceTab;
  month?: string;
  departmentId?: number;
}

const MONTH = /^\d{4}-(0[1-9]|1[0-2])$/;

/** The host route's `validateSearch` for Absence; what a hand-edited link invents is left out. */
export const validateAbsenceSearch = (search: Record<string, unknown>): AbsenceSearch => {
  const out: AbsenceSearch = {};
  if (absenceTabs.includes(search.tab as AbsenceTab)) out.tab = search.tab as AbsenceTab;
  if (typeof search.month === "string" && MONTH.test(search.month)) out.month = search.month;
  const departmentId = positiveId(search.departmentId);
  if (departmentId !== undefined) out.departmentId = departmentId;
  return out;
};

/** The Competence area's tabs (design §7, delivery C): the matrix, and HR's interests overview. */
export const competenceTabs = ["matrix", "interests"] as const;
export type CompetenceTab = (typeof competenceTabs)[number];

/**
 * The Competence area's URL search: the tab and the matrix's five filters
 * (design S14), so a filtered matrix survives a refresh and travels in a link.
 */
export interface CompetenceSearch {
  tab?: CompetenceTab;
  categoryId?: number;
  competenceId?: number[];
  minLevel?: number;
  validOn?: string;
  verifiedOnly?: boolean;
}

/** Ids from a repeated parameter, a comma-joined one or a single one; anything else is left out. */
const positiveIds = (value: unknown): number[] => {
  const parts = Array.isArray(value) ? value : typeof value === "string" ? value.split(",") : [value];
  return [...new Set(parts.map(positiveId).filter((id): id is number => id !== undefined))];
};

/** The host route's `validateSearch` for the Competence area; what a hand-edited link invents is left out. */
export const validateCompetenceSearch = (search: Record<string, unknown>): CompetenceSearch => {
  const out: CompetenceSearch = {};
  if (competenceTabs.includes(search.tab as CompetenceTab)) out.tab = search.tab as CompetenceTab;
  const categoryId = positiveId(search.categoryId);
  if (categoryId !== undefined) out.categoryId = categoryId;
  const competenceId = search.competenceId === undefined ? [] : positiveIds(search.competenceId);
  if (competenceId.length > 0) out.competenceId = competenceId;
  const minLevel = search.minLevel === undefined || search.minLevel === "" ? Number.NaN : Number(search.minLevel);
  if (Number.isInteger(minLevel) && minLevel >= 0 && minLevel <= 4) out.minLevel = minLevel;
  if (isIsoDate(search.validOn)) out.validOn = search.validOn;
  if (search.verifiedOnly === true || search.verifiedOnly === "true") out.verifiedOnly = true;
  return out;
};
