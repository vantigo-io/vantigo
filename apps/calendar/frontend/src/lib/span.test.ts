import { describe, expect, it } from "vitest";
import { shiftSeries } from "./span";

describe("shiftSeries", () => {
  it("moves a timed series by what its occurrence moved", () => {
    expect(
      shiftSeries(
        { allDay: false, startsAt: "2026-10-06T07:00:00.000Z", endsAt: "2026-10-06T08:00:00.000Z" },
        { allDay: false, startsAt: "2026-10-20T07:00:00.000Z", endsAt: "2026-10-20T08:00:00.000Z" },
        { allDay: false, startsAt: "2026-10-20T08:30:00.000Z", endsAt: "2026-10-20T10:00:00.000Z" },
      ),
    ).toEqual({ allDay: false, startsAt: "2026-10-06T08:30:00.000Z", endsAt: "2026-10-06T10:00:00.000Z" });
  });

  it("moves an all-day series by days", () => {
    expect(
      shiftSeries(
        { allDay: true, startDate: "2026-10-05", endDate: "2026-10-05" },
        { allDay: true, startDate: "2026-10-19", endDate: "2026-10-19" },
        { allDay: true, startDate: "2026-10-20", endDate: "2026-10-21" },
      ),
    ).toEqual({ allDay: true, startDate: "2026-10-06", endDate: "2026-10-07" });
  });
});
