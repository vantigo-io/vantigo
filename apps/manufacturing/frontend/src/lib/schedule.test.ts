import { describe, expect, it } from "vitest";
import { scheduleBarFixture, scheduleFixture } from "../test/server";
import {
  addDays,
  barLayout,
  dayIndexOf,
  dayWindows,
  draggedStart,
  fractionOf,
  instantAt,
  localDateAndTime,
  localInstant,
  movedStart,
  rescheduleBody,
  snapToGrid,
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

  it("fall back to 06:00–22:00 in the viewer's time on a day nothing is open", () => {
    expect(windows[3]).toEqual({
      date: "2026-11-14",
      start: localInstant("2026-11-14", "06:00"),
      end: localInstant("2026-11-14", "22:00"),
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

  it("reads Move to…'s day and time in the viewer's zone, snapped", () => {
    expect(movedStart("2026-11-12", "07:10")).toBe(localInstant("2026-11-12", "07:15"));
    const local = localDateAndTime(new Date(localInstant("2026-11-12", "07:30")).toISOString());
    expect(local).toEqual({ date: "2026-11-12", time: "07:30" });
  });

  it("counts days on the date alone", () => {
    expect(addDays("2026-11-11", 7)).toBe("2026-11-18");
    expect(addDays("2026-03-02", -7)).toBe("2026-02-23");
  });
});
