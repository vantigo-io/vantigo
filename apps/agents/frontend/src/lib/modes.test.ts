import { describe, expect, it } from "vitest";
import { charsLeft, clampThreshold, raisesMode, replyModes, scheduleDays, thresholdApplies } from "./modes";

describe("the reply modes", () => {
  it("offers review, auto above a score and auto, in that order", () => {
    expect(replyModes).toEqual(["review", "auto_above", "auto"]);
  });

  it("calls a move towards auto a raise, and a move back not", () => {
    expect(raisesMode("review", "auto_above")).toBe(true);
    expect(raisesMode("review", "auto")).toBe(true);
    expect(raisesMode("auto_above", "auto")).toBe(true);
    expect(raisesMode("auto", "review")).toBe(false);
    expect(raisesMode("auto_above", "auto_above")).toBe(false);
  });

  it("reads the threshold only for auto above, and keeps it between 50 and 100", () => {
    expect(thresholdApplies("auto_above")).toBe(true);
    expect(thresholdApplies("auto")).toBe(false);
    expect(clampThreshold(10)).toBe(50);
    expect(clampThreshold(87.6)).toBe(88);
    expect(clampThreshold(400)).toBe(100);
  });

  it("counts the characters left of a limit", () => {
    expect(charsLeft("abc", 10)).toBe(7);
    expect(charsLeft("abcd", 3)).toBe(-1);
  });
});

describe("the days a schedule can name", () => {
  it("are the seven weekdays for a weekly one and the first 28 days for a monthly one, which every month has", () => {
    expect(scheduleDays("weekly")).toEqual([1, 2, 3, 4, 5, 6, 7]);
    expect(scheduleDays("monthly")).toHaveLength(28);
    expect(scheduleDays("monthly").at(-1)).toBe(28);
    expect(scheduleDays("none")).toEqual([]);
  });
});
