import type { ReplyMode } from "../api/types";

/** The limits the contract states for what a person types. */
export const NAME_MAX = 60;
export const STYLE_MAX = 1500;
export const INSTRUCTIONS_MAX = 8000;
export const THRESHOLD_MIN = 50;
export const THRESHOLD_MAX = 100;
export const RETENTION_MIN = 1;
export const RETENTION_MAX = 60;
/** The least a daily token budget may be; 0 removes it. */
export const BUDGET_MIN = 1000;

export const replyModes: readonly ReplyMode[] = ["review", "auto_above", "auto"];

const rank: Record<ReplyMode, number> = { review: 0, auto_above: 1, auto: 2 };

/** Whether moving from one mode to another gives the agent more freedom: that is the Owner's alone (V12). */
export const raisesMode = (from: ReplyMode, to: ReplyMode): boolean => rank[to] > rank[from];

/** The threshold is only read in the "auto above" mode. */
export const thresholdApplies = (mode: ReplyMode): boolean => mode === "auto_above";

export const clampThreshold = (value: number): number =>
  Math.min(THRESHOLD_MAX, Math.max(THRESHOLD_MIN, Math.round(value)));

/** How many characters are left of a limit; negative when over it. */
export const charsLeft = (text: string, max: number): number => max - text.length;
