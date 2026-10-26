import { isIsoDate } from "./weeks";

/**
 * How long a picker waits after the last keystroke before it searches. The
 * tests run with none, so what they wait on is the stub's answer, not a timer.
 */
export const SEARCH_DEBOUNCE_MS = import.meta.env.MODE === "test" ? 0 : 250;

/** The window lengths the board offers, in weeks; twelve is the default (design §7). */
export const boardWeekCounts = [4, 8, 12, 26] as const;
export const BOARD_DEFAULT_WEEKS = 12;
/** The most weeks the board answers (S10). */
export const BOARD_MAX_WEEKS = 53;

export const boardGroupings = ["people", "project"] as const;
export type BoardGrouping = (typeof boardGroupings)[number];

/**
 * The board's URL search (S10). Every control lives in the URL so the board
 * survives a refresh and travels in a pasted link; the page reads it with
 * `useSearch` and writes it with `navigate`. A switch is in the URL only when
 * it is off — on is the server's default.
 */
export interface BoardSearch {
  /** The window's first day; the page shows the weeks from its Monday. Left out, this week. */
  from?: string;
  /** The window's last day; left out, twelve weeks from `from`. */
  to?: string;
  /** Left out, people. */
  groupBy?: BoardGrouping;
  departmentId?: number;
  /** Only the people holding this competence on `from`; there is no control for it, a link may carry it. */
  competenceId?: number;
  projectId?: number;
  /** `false`: tentative rows are neither summed nor listed (Show tentative off). */
  tentative?: boolean;
  /** `false`: rows on planned projects are left out (Include pipeline off). */
  planned?: boolean;
  /** A form to open on arrival — the seam the host's Spotlight "New allocation" action lands on. */
  create?: "allocation";
}

const positiveId = (value: unknown): number | undefined => {
  const id = Number(value);
  return Number.isInteger(id) && id > 0 ? id : undefined;
};

const isOff = (value: unknown) => value === false || value === "false";

/**
 * The host route's `validateSearch` for the board, exported so the route and
 * this package's tests validate the URL the same way. Anything a hand-edited
 * link invents falls back to "no filter" rather than reaching the API as a
 * value it would refuse with a 400.
 */
export const validateBoardSearch = (search: Record<string, unknown>): BoardSearch => {
  const out: BoardSearch = {};
  if (isIsoDate(search.from)) out.from = search.from;
  if (isIsoDate(search.to) && (out.from === undefined || search.to >= out.from)) out.to = search.to;
  if (search.groupBy === "project") out.groupBy = "project";
  const departmentId = positiveId(search.departmentId);
  if (departmentId !== undefined) out.departmentId = departmentId;
  const competenceId = positiveId(search.competenceId);
  if (competenceId !== undefined) out.competenceId = competenceId;
  const projectId = positiveId(search.projectId);
  if (projectId !== undefined) out.projectId = projectId;
  if (isOff(search.tentative)) out.tentative = false;
  if (isOff(search.planned)) out.planned = false;
  if (search.create === "allocation") out.create = "allocation";
  return out;
};

/** The URL search of a page over a window — the project's plan, the person's timeline: its first and last day. */
export interface WindowSearch {
  from?: string;
  to?: string;
}

/** The host route's `validateSearch` for the project's plan and the person's timeline. */
export const validateWindowSearch = (search: Record<string, unknown>): WindowSearch => {
  const out: WindowSearch = {};
  if (isIsoDate(search.from)) out.from = search.from;
  if (isIsoDate(search.to) && (out.from === undefined || search.to >= out.from)) out.to = search.to;
  return out;
};
