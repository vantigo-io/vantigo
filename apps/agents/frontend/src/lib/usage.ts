import type { AgentUsageDay } from "../api/types";

export interface UsageTotals {
  runs: number;
  tokens: number;
  sent: number;
  held: number;
  discarded: number;
}

/** The sums of the days: runs, tokens (input and output), and what was sent, held and discarded. */
export const totalsOf = (days: readonly AgentUsageDay[]): UsageTotals =>
  days.reduce<UsageTotals>(
    (sum, day) => ({
      runs: sum.runs + day.runs,
      tokens: sum.tokens + day.inputTokens + day.outputTokens,
      sent: sum.sent + day.sent,
      held: sum.held + day.held,
      discarded: sum.discarded + day.discarded,
    }),
    { runs: 0, tokens: 0, sent: 0, held: 0, discarded: 0 },
  );

/** How much of the daily token budget a day used, 0 to 1 and beyond; null for an agent with no budget. */
export const budgetShare = (day: AgentUsageDay | undefined, budget: number | null | undefined): number | null =>
  day && budget ? (day.inputTokens + day.outputTokens) / budget : null;
