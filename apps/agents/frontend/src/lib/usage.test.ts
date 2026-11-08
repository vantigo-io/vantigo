import { describe, expect, it } from "vitest";
import type { AgentUsageDay } from "../api/types";
import { budgetShare, totalsOf } from "./usage";

const day = (patch: Partial<AgentUsageDay>): AgentUsageDay => ({
  day: "2026-11-08",
  runs: 0,
  inputTokens: 0,
  outputTokens: 0,
  sent: 0,
  held: 0,
  discarded: 0,
  ...patch,
});

describe("the usage figures", () => {
  it("adds the days", () => {
    expect(totalsOf([])).toEqual({ runs: 0, tokens: 0, sent: 0, held: 0, discarded: 0 });
    expect(
      totalsOf([
        day({ runs: 2, inputTokens: 100, outputTokens: 20, sent: 1, held: 1 }),
        day({ runs: 1, inputTokens: 50, outputTokens: 5, discarded: 1 }),
      ]),
    ).toEqual({ runs: 3, tokens: 175, sent: 1, held: 1, discarded: 1 });
  });

  it("states a day against the budget, when there is one", () => {
    expect(budgetShare(day({ inputTokens: 600, outputTokens: 400 }), 2000)).toBe(0.5);
    expect(budgetShare(day({ inputTokens: 3000 }), 2000)).toBe(1.5);
    expect(budgetShare(day({}), null)).toBeNull();
    expect(budgetShare(undefined, 2000)).toBeNull();
  });
});
