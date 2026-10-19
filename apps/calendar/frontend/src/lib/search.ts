import { isCivilDate } from "./dates";

export const calendarViews = ["month", "week", "day", "list"] as const;
export type CalendarView = (typeof calendarViews)[number];

/** The view a URL without one opens on — the page's own call under phone width is the list. */
export const DEFAULT_CALENDAR_VIEW: CalendarView = "month";

/**
 * The calendar page's URL search (design D8). Everything the page shows lives
 * here, so a refresh or a pasted link opens the same calendar.
 */
export interface CalendarSearch {
  /**
   * The grid. Left out when the URL names none, so the page can choose:
   * `DEFAULT_CALENDAR_VIEW`, or the list on a phone, where the grids do not fit.
   */
  view?: CalendarView;
  /** A day in the period shown, in the viewer's zone. */
  date?: string;
  /** A colleague whose calendar is shown instead of the caller's, read-only. */
  user?: string;
  /** An appointment whose sheet is open. */
  appointment?: number;
  /** With `appointment`: the occurrence of the series it opened on, by the day the series put it on. */
  occurrence?: string;
  /** Opens a new appointment's sheet on arrival — the dashboard's and the record pages' action. */
  new?: 1;
  /** The customer a new appointment is made from. */
  customerId?: number;
  /** The project a new appointment is made from. */
  projectId?: number;
}

const positiveId = (value: unknown): number | undefined => {
  const id = Number(value);
  return typeof value !== "boolean" && value !== "" && Number.isInteger(id) && id > 0 ? id : undefined;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * The host route's `validateSearch`, exported so the route and this package's
 * tests read the URL the same way. Anything a hand-edited link invents falls
 * back to "not given" rather than reaching the API as a value it refuses.
 */
export const validateCalendarSearch = (search: Record<string, unknown>): CalendarSearch => {
  const appointment = positiveId(search.appointment);
  return {
    view: calendarViews.includes(search.view as CalendarView) ? (search.view as CalendarView) : undefined,
    date: isCivilDate(search.date) ? search.date : undefined,
    user: typeof search.user === "string" && uuid.test(search.user) ? search.user : undefined,
    appointment,
    occurrence: appointment !== undefined && isCivilDate(search.occurrence) ? search.occurrence : undefined,
    new: [1, "1", true, "true"].includes(search.new as never) ? 1 : undefined,
    customerId: positiveId(search.customerId),
    projectId: positiveId(search.projectId),
  };
};
