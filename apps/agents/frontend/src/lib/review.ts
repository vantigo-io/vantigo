import type { DiscardReason } from "../api/types";

/**
 * The codes a review item's `heldBy` may carry (V18, V20): the hard rules, the
 * content check, and the machinery's own holds. Each is worded in the catalog
 * as `held.<code>`; a code this build does not know reads as `held.other`.
 */
export const heldCodes = [
  "S1",
  "S2",
  "S3",
  "S4",
  "S5",
  "S6",
  "S7",
  "C1",
  "C2",
  "CHECK",
  "CHANGE",
  "SEND",
  "TAKEN",
  "MOVED",
] as const;

export const heldKey = (code: string): string =>
  heldCodes.some((known) => known === code) ? `held.${code}` : "held.other";

/** The reasons a reviewer may give when discarding (V20), in the order the dialog lists them. */
export const discardReasons: readonly DiscardReason[] = ["wrong_facts", "wrong_tone", "should_not_answer", "other"];

/** The reasons a withdrawn item gives (the `outcome` of the item), each worded in the catalog as `outcome.<code>`. */
export const outcomeCodes = [
  "conversation_taken",
  "conversation_moved",
  "window_closed",
  "not_allowed",
  "agent_inactive",
  "agent_gone",
  "role_gone",
  "conversation_unreadable",
  "conversation_gone",
  "unknown",
  "already_sent",
] as const;

export const outcomeKey = (code: string): string =>
  outcomeCodes.some((known) => known === code) ? `outcome.${code}` : "outcome.other";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** Milliseconds from `now` to an instant; 0 once it has passed, null for none. */
export const msUntil = (instant: string | null | undefined, now: number): number | null => {
  if (!instant) return null;
  const at = Date.parse(instant);
  if (Number.isNaN(at)) return null;
  return Math.max(0, at - now);
};

/** What is left, in the two largest units that matter: days and hours, hours and minutes, minutes, or under a minute. */
export type Remaining =
  | { kind: "daysHours"; days: number; hours: number }
  | { kind: "hoursMinutes"; hours: number; minutes: number }
  | { kind: "minutes"; minutes: number }
  | { kind: "lessThanMinute" };

export const remaining = (ms: number): Remaining => {
  if (ms >= DAY) return { kind: "daysHours", days: Math.floor(ms / DAY), hours: Math.floor((ms % DAY) / HOUR) };
  if (ms >= HOUR)
    return { kind: "hoursMinutes", hours: Math.floor(ms / HOUR), minutes: Math.floor((ms % HOUR) / MINUTE) };
  if (ms >= MINUTE) return { kind: "minutes", minutes: Math.floor(ms / MINUTE) };
  return { kind: "lessThanMinute" };
};

/** An item that lapses within the hour is urgent; a Messenger window countdown always is. */
export const isUrgent = (expiresAt: string, windowClosesAt: string | null | undefined, now: number): boolean =>
  Boolean(windowClosesAt) || (msUntil(expiresAt, now) ?? Number.POSITIVE_INFINITY) < HOUR;
