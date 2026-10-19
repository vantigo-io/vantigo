import { describe, expect, it } from "vitest";
import type { CalendarItem } from "../api/items";
import { holidayEventInput, toEventInput } from "./items";

const options = { busyLabel: "Busy" };

describe("toEventInput", () => {
  it("draws an all-day item to the day after its last, FullCalendar's end being exclusive", () => {
    const item: CalendarItem = {
      kind: "staff.absence",
      id: "9",
      title: "Holiday",
      allDay: true,
      startDate: "2026-10-26",
      endDate: "2026-10-28",
      done: false,
      path: "/staff/me?tab=absence",
    };
    expect(toEventInput(item, options)).toMatchObject({
      id: "staff.absence:9",
      allDay: true,
      start: "2026-10-26",
      end: "2026-10-29",
      color: "var(--vantigo-calendar-teal)",
      editable: false,
      extendedProps: { item },
    });
  });

  it("draws a timed item at its instants, a point in time without an end", () => {
    const item: CalendarItem = {
      kind: "customers.interaction",
      id: "4",
      title: "Call",
      allDay: false,
      startsAt: "2026-10-20T08:00:00Z",
      endsAt: "2026-10-20T08:00:00Z",
      done: false,
    };
    const event = toEventInput(item, options);
    expect(event).toMatchObject({ start: "2026-10-20T08:00:00Z", color: "var(--vantigo-calendar-blue)" });
    expect(event.end).toBeUndefined();
  });

  it("strikes a done item, lets the owner drag their own appointment and marks a series", () => {
    const done = toEventInput(
      { kind: "projects.task", id: "1", title: "Ship", allDay: true, startDate: "2026-10-20", done: true },
      options,
    );
    expect(done.className).toContain("vantigo-calendar-done");
    const own = toEventInput(
      {
        kind: "calendar.appointment",
        id: "1001:2026-10-20",
        title: "Standup",
        allDay: false,
        startsAt: "2026-10-20T07:00:00Z",
        endsAt: "2026-10-20T07:15:00Z",
        done: false,
        appointment: {
          id: 1001,
          isOwner: true,
          occurrenceDate: "2026-10-20",
          recurring: true,
          transparency: "free",
          visibility: "shared",
        },
      },
      options,
    );
    expect(own).toMatchObject({ editable: true, color: "var(--vantigo-calendar-primary)" });
    expect(own.className).toContain("vantigo-calendar-recurring");
    expect(own.className).toContain("vantigo-calendar-free");
  });

  it("names a colleague's private appointment Busy, hatched and not movable", () => {
    const event = toEventInput(
      {
        kind: "calendar.appointment",
        id: "7",
        title: "",
        allDay: false,
        startsAt: "2026-10-20T07:00:00Z",
        endsAt: "2026-10-20T08:00:00Z",
        done: false,
        appointment: { id: 7, isOwner: false, recurring: false, transparency: "busy", visibility: "private" },
      },
      { busyLabel: "Busy", readOnly: true },
    );
    expect(event).toMatchObject({ title: "Busy", editable: false });
    expect(event.className).toContain("vantigo-calendar-busy");
  });

  it("draws a public holiday behind the day in grey", () => {
    expect(holidayEventInput({ date: "2026-12-25", name: "Første juledag" })).toMatchObject({
      start: "2026-12-25",
      allDay: true,
      display: "background",
      color: "var(--vantigo-calendar-gray)",
    });
  });
});
