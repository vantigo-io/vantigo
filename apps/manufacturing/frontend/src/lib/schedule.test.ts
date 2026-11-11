import { describe, expect, it } from "vitest";
import { scheduleBarFixture, scheduleFixture } from "../test/server";
import {
  addDays,
  barLayout,
  dateAndTimeIn,
  dayIndexOf,
  dayWindows,
  draggedStart,
  fractionOf,
  instantAt,
  movedStart,
  moveToPrefill,
  rescheduleBody,
  snapToGrid,
  workOrderStart,
} from "./schedule";

const at = (iso: string) => new Date(iso).getTime();

describe("the board's day columns", () => {
  const windows = dayWindows(scheduleFixture());

  it("span each day's open time across the work centers", () => {
    expect(windows).toHaveLength(7);
    expect(windows[0]).toEqual({
      date: "2026-11-11",
      start: at("2026-11-11T06:00:00Z"),
      end: at("2026-11-11T14:00:00Z"),
    });
  });

  it("fall back to 06:00–22:00 in the business time zone on a day nothing is open, whatever the browser's", () => {
    expect(dayWindows(scheduleFixture(), "America/New_York")[3]).toEqual({
      date: "2026-11-14",
      start: at("2026-11-14T11:00:00Z"),
      end: at("2026-11-15T03:00:00Z"),
    });
    expect(dayWindows(scheduleFixture(), "Europe/Oslo")[3]).toEqual({
      date: "2026-11-14",
      start: at("2026-11-14T05:00:00Z"),
      end: at("2026-11-14T21:00:00Z"),
    });
  });

  it("map an instant to its place across the row and back", () => {
    expect(fractionOf(at("2026-11-11T10:00:00Z"), windows)).toBeCloseTo(0.5 / 7);
    expect(fractionOf(at("2026-11-12T06:00:00Z"), windows)).toBeCloseTo(1 / 7);
    expect(instantAt(1.5 / 7, windows)).toBe(at("2026-11-12T10:00:00Z"));
    expect(dayIndexOf(at("2026-11-13T07:00:00Z"), windows)).toBe(2);
    // Before the first column it is the first column's.
    expect(dayIndexOf(at("2026-11-01T07:00:00Z"), windows)).toBe(0);
  });

  it("place a bar in the column its start falls in", () => {
    const layout = barLayout(scheduleBarFixture(), windows);
    expect(layout.index).toBe(0);
    expect(layout.left).toBeCloseTo(0.25 / 7);
    expect(layout.width).toBeCloseTo(0.125 / 7);
  });
});

describe("moving a bar", () => {
  const windows = dayWindows(scheduleFixture());

  it("snaps to the quarter hour", () => {
    expect(snapToGrid(at("2026-11-11T08:07:00Z"))).toBe(at("2026-11-11T08:00:00Z"));
    expect(snapToGrid(at("2026-11-11T08:08:00Z"))).toBe(at("2026-11-11T08:15:00Z"));
  });

  it("starts a dragged bar where its left edge lands, a day further for a column's travel", () => {
    const bar = scheduleBarFixture();
    expect(draggedStart(bar, 1 / 7, windows)).toBe(at("2026-11-12T08:00:00Z"));
    // A sixteenth of a column is half an hour of an eight-hour day.
    expect(draggedStart(bar, 1 / 7 / 16, windows)).toBe(at("2026-11-11T08:30:00Z"));
  });

  it("asks for the start under the work order's revision", () => {
    expect(rescheduleBody(scheduleBarFixture({ workOrderRevision: 9 }), at("2026-11-12T08:00:00Z"))).toEqual({
      start: "2026-11-12T08:00:00.000Z",
      revision: 9,
    });
  });

  it("reads Move to…'s day and time in the viewer's zone, not the browser's, snapped", () => {
    expect(movedStart("2026-11-12", "07:10", "Asia/Tokyo")).toBe(at("2026-11-11T22:15:00Z"));
    expect(movedStart("2026-11-12", "07:10", "Europe/Oslo")).toBe(at("2026-11-12T06:15:00Z"));
    expect(dateAndTimeIn("2026-11-11T22:30:00Z", "Asia/Tokyo")).toEqual({ date: "2026-11-12", time: "07:30" });
  });

  it("prefills Move to… with the work order's start, not the piece's (M36)", () => {
    const later = scheduleBarFixture({ startsAt: "2026-11-12T08:00:00Z", plannedStart: "2026-11-11T13:00:00Z" });
    expect(moveToPrefill(later, "UTC")).toEqual({ date: "2026-11-11", time: "13:00" });
  });

  it("moves the whole work order as far as a later piece was dragged (M36)", () => {
    const later = scheduleBarFixture({ startsAt: "2026-11-12T08:00:00Z", plannedStart: "2026-11-11T13:00:00Z" });
    expect(workOrderStart(later, at("2026-11-13T08:00:00Z"))).toBe(at("2026-11-12T13:00:00Z"));
    // The first piece starts where the work order does: the drop is the start.
    expect(workOrderStart(scheduleBarFixture(), at("2026-11-12T08:00:00Z"))).toBe(at("2026-11-12T08:00:00Z"));
  });

  it("counts days on the date alone", () => {
    expect(addDays("2026-11-11", 7)).toBe("2026-11-18");
    expect(addDays("2026-03-02", -7)).toBe("2026-02-23");
  });
});
