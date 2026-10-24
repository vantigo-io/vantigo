import type { StaffCompetence, StaffCompetenceKind } from "../api/competences";
import { fromUtc, isIsoDate, toUtc } from "./dates";

/** A translation function, as `useI18n` hands one out. */
type Translate = (key: string, values?: Record<string, unknown>) => string;

/** The one level scale (design S2), 0 to 4, fixed in code and named in both languages. */
export const LEVELS = [0, 1, 2, 3, 4] as const;
export const competenceKinds: readonly StaffCompetenceKind[] = ["skill", "certificate"];
export const competenceBases = ["evidence", "observed"] as const;
export const competenceSources = ["self", "manager", "hr"] as const;

export const levelLabel = (t: Translate, level: number): string => t(`level_${level}`);
export const kindLabel = (t: Translate, kind: string): string => t(`competenceKind_${kind}`);
export const basisLabel = (t: Translate, basis: string): string => t(`competenceBasis_${basis}`);
export const sourceLabel = (t: Translate, source: string): string => t(`competenceSource_${source}`);

/** The levels as a select's options. */
export const levelOptions = (t: Translate) =>
  LEVELS.map((level) => ({ value: String(level), label: levelLabel(t, level) }));

/**
 * The expiry proposed for a certificate issued on `issued` with a validity
 * of `months` (design S5): the same day so many months on, the month's last
 * day when it has fewer days. Nothing when either is unknown.
 */
export const proposedExpiry = (issued: string | null | undefined, months: number | undefined): string | undefined => {
  if (!isIsoDate(issued) || !months) return undefined;
  const start = toUtc(issued);
  const target = new Date(Date.UTC(start.getUTCFullYear(), start.getUTCMonth() + months, 1));
  const lastDay = new Date(Date.UTC(target.getUTCFullYear(), target.getUTCMonth() + 1, 0)).getUTCDate();
  target.setUTCDate(Math.min(start.getUTCDate(), lastDay));
  return fromUtc(target);
};

/** The calendar days from `today` to `day`, negative once it has passed. */
export const daysUntil = (today: string, day: string): number =>
  Math.round((toUtc(day).getTime() - toUtc(today).getTime()) / 86_400_000);

/** The lead the server gives a validity without one (design S7): 30 days to 12 months, 60 to 24, 180 beyond. */
export const defaultLeadDays = (validityMonths: number): number =>
  validityMonths <= 12 ? 30 : validityMonths <= 24 ? 60 : 180;

/** A catalogue row's name in the reader's language, English when the language has none. */
export const competenceName = (row: { names: { en: string; nb: string } }, locale: string): string =>
  (row.names as Record<string, string>)[locale] ?? row.names.en;

/** The catalogue a new row may name: neither hidden nor archived. */
export const offeredCompetences = (catalogue: readonly StaffCompetence[]) =>
  catalogue.filter((one) => !one.hidden && !one.archivedAt);
