import { addDays, daysBetween } from "./dates";

/** When an appointment or one occurrence is: two civil days, inclusive, or two instants, the end exclusive. */
export type Span =
  | { allDay: true; startDate: string; endDate: string }
  | { allDay: false; startsAt: string; endsAt: string };

/** The span an appointment or an item carries, whichever pair it has. */
export const spanOf = (value: {
  allDay: boolean;
  startDate?: string;
  endDate?: string;
  startsAt?: string;
  endsAt?: string;
}): Span =>
  value.allDay
    ? { allDay: true, startDate: value.startDate ?? "", endDate: value.endDate ?? value.startDate ?? "" }
    : { allDay: false, startsAt: value.startsAt ?? "", endsAt: value.endsAt ?? value.startsAt ?? "" };

const ms = (instant: string) => Date.parse(instant);
const iso = (value: number) => new Date(value).toISOString();

/**
 * The series' new span when one of its occurrences was moved from `from` to
 * `to` and the person said "the series": the series' own start and end move
 * by what the occurrence's did. A change between all-day and timed has no
 * offset to carry, so the series takes the new span as it is.
 */
export const shiftSeries = (series: Span, from: Span, to: Span): Span => {
  if (series.allDay !== to.allDay || from.allDay !== to.allDay) return to;
  if (series.allDay && from.allDay && to.allDay)
    return {
      allDay: true,
      startDate: addDays(series.startDate, daysBetween(from.startDate, to.startDate)),
      endDate: addDays(series.endDate, daysBetween(from.endDate, to.endDate)),
    };
  if (!series.allDay && !from.allDay && !to.allDay)
    return {
      allDay: false,
      startsAt: iso(ms(series.startsAt) + ms(to.startsAt) - ms(from.startsAt)),
      endsAt: iso(ms(series.endsAt) + ms(to.endsAt) - ms(from.endsAt)),
    };
  return to;
};

export const sameSpan = (a: Span, b: Span): boolean =>
  a.allDay && b.allDay
    ? a.startDate === b.startDate && a.endDate === b.endDate
    : !a.allDay && !b.allDay && ms(a.startsAt) === ms(b.startsAt) && ms(a.endsAt) === ms(b.endsAt);

/** The span's pair as the API's write bodies name it. */
export const spanFields = (span: Span) =>
  span.allDay ? { startDate: span.startDate, endDate: span.endDate } : { startsAt: span.startsAt, endsAt: span.endsAt };
