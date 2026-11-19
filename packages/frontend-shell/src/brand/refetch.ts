/** The longest the client waits before asking for its brand again (brands design B24). */
export const BRAND_REFETCH_MAX_MS = 6 * 60 * 60 * 1000;

/** The shortest wait, so a boundary already passed (or a skewed clock) cannot make a request loop. */
export const BRAND_REFETCH_MIN_MS = 30 * 1000;

/**
 * How long to wait before asking for the brand again: `min(validUntil - now, 6 h)`, at least 30
 * seconds, or null when the payload names no boundary. A browser timer fires at once for a delay
 * past 2^31 ms (about 24.8 days), which a campaign starting in two months would be; the cap keeps
 * every delay far below it.
 */
export function brandRefetchDelay(validUntil: string | null | undefined, now: number): number | null {
  if (!validUntil) return null;
  const at = Date.parse(validUntil);
  if (Number.isNaN(at)) return null;
  return Math.min(Math.max(at - now, BRAND_REFETCH_MIN_MS), BRAND_REFETCH_MAX_MS);
}
