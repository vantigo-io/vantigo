import { describe, expect, it } from "vitest";
import { type TimeLogValues, timeLogBody, timeLogProblems } from "./time-log-body";

const NOW = new Date("2026-11-11T12:00").getTime();
const valid: TimeLogValues = {
  startedAt: "2026-11-11T08:00",
  endedAt: "2026-11-11T09:30",
  quantity: "",
  note: "Forgot",
};

describe("a time log's rules (M29)", () => {
  it("accepts an end after the start, in the past, with a note", () => {
    expect(timeLogProblems(valid, NOW)).toEqual({});
  });

  it("wants both times and a note", () => {
    expect(timeLogProblems({ startedAt: "", endedAt: "", quantity: "", note: " " }, NOW)).toEqual({
      startedAt: "required",
      endedAt: "required",
      note: "required",
    });
  });

  it("refuses an end at or before the start, and one in the future", () => {
    expect(timeLogProblems({ ...valid, endedAt: valid.startedAt }, NOW).endedAt).toBe("endBeforeStart");
    expect(timeLogProblems({ ...valid, endedAt: "2026-11-11T07:00" }, NOW).endedAt).toBe("endBeforeStart");
    expect(timeLogProblems({ ...valid, endedAt: "2026-11-11T13:00" }, NOW).endedAt).toBe("endInFuture");
  });

  it("refuses a quantity that is not a decimal and a note past 2,000 characters", () => {
    expect(timeLogProblems({ ...valid, quantity: "abc" }, NOW).quantity).toBe("quantity");
    expect(timeLogProblems({ ...valid, note: "x".repeat(2001) }, NOW).note).toBe("noteTooLong");
  });

  it("sends instants, the note trimmed and an empty quantity as none", () => {
    expect(timeLogBody({ ...valid, note: " Forgot ", quantity: "" })).toEqual({
      startedAt: new Date("2026-11-11T08:00").toISOString(),
      endedAt: new Date("2026-11-11T09:30").toISOString(),
      note: "Forgot",
      quantity: null,
    });
    expect(timeLogBody({ ...valid, quantity: "2,5" }).quantity).toBe("2.5");
  });
});
