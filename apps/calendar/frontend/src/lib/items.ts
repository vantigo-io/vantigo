import type { EventInput } from "@fullcalendar/react";
import type { CalendarItem, CalendarPublicHoliday } from "../api/items";
import { colourForKind, colourForModule, colourForName, HOLIDAYS_KIND } from "./colours";
import { addDays } from "./dates";

/** What an event drawn from the API carries back to the page's handlers. */
export interface CalendarEventProps {
  item?: CalendarItem;
  holiday?: CalendarPublicHoliday;
}

export interface ToEventOptions {
  /**
   * The words for an item whose title the server leaves blank: a colleague's
   * private appointment, a busy-only subscription's item.
   */
  busyLabel: string;
  /** A colleague's calendar: nothing on it is the viewer's to move. */
  readOnly?: boolean;
}

/**
 * One item as FullCalendar draws it. An all-day item's last day is inclusive
 * on the wire and FullCalendar's end is exclusive, so the end is the day
 * after; a timed one is its two instants, a point in time drawn at the
 * default length. Only the caller's own appointment may be dragged; a
 * subscription's item is drawn in its subscription's colour (feed design D9).
 */
export const toEventInput = (item: CalendarItem, { busyLabel, readOnly = false }: ToEventOptions): EventInput => {
  const appointment = item.appointment;
  const subscription = item.subscription;
  const isPrivate = appointment?.visibility === "private" || subscription?.busyOnly === true;
  const declined = appointment?.myStatus === "declined";
  const className = [
    "vantigo-calendar-item",
    item.done || declined ? "vantigo-calendar-done" : "",
    appointment?.transparency === "free" ? "vantigo-calendar-free" : "",
    readOnly && isPrivate ? "vantigo-calendar-busy" : "",
    appointment?.recurring ? "vantigo-calendar-recurring" : "",
  ]
    .filter(Boolean)
    .join(" ");
  const times = item.allDay
    ? {
        start: item.startDate,
        end: addDays(item.endDate ?? item.startDate ?? "", 1),
      }
    : {
        start: item.startsAt,
        ...(item.endsAt && item.endsAt !== item.startsAt ? { end: item.endsAt } : {}),
      };
  return {
    id: `${item.kind}:${item.id}`,
    title: item.title.trim() ? item.title : isPrivate ? busyLabel : item.title,
    allDay: item.allDay,
    ...times,
    color: subscription ? colourForName(subscription.colour) : colourForKind(item.kind),
    className,
    editable: !readOnly && appointment?.isOwner === true,
    extendedProps: { item } satisfies CalendarEventProps,
  };
};

/** A public holiday, drawn behind the day in grey. */
export const holidayEventInput = (holiday: CalendarPublicHoliday): EventInput => ({
  id: `${HOLIDAYS_KIND}:${holiday.date}`,
  title: holiday.name,
  start: holiday.date,
  allDay: true,
  display: "background",
  color: colourForModule("holidays"),
  className: "vantigo-calendar-holiday",
  editable: false,
  extendedProps: { holiday } satisfies CalendarEventProps,
});
