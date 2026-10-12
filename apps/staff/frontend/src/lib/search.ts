import type { PeopleStatusFilter } from "../api/people";

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

/** The tabs of the person page and My page (design §7); the Account and Absence tabs come with later deliveries. */
export const personTabs = ["profile", "employments", "history", "documents"] as const;
export type PersonTab = (typeof personTabs)[number];

/** The person page's and My page's URL search: the tab, so a refresh or a pasted link opens the same view. */
export interface PersonSearch {
  tab?: PersonTab;
}

/** The host route's `validateSearch` for the person page and My page; an unknown tab is the profile. */
export const validatePersonSearch = (search: Record<string, unknown>): PersonSearch => ({
  tab: personTabs.includes(search.tab as PersonTab) && search.tab !== "profile" ? (search.tab as PersonTab) : undefined,
});
