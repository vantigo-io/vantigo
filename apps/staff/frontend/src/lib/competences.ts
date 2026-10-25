import type {
  StaffAvailabilityDeclaration,
  StaffAvailabilityKind,
  StaffCompetence,
  StaffCompetenceGapStatus,
  StaffCompetenceKind,
  StaffPreferenceKind,
} from "../api/competences";
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

/** The seven preference kinds (design S9), in the table's order. */
export const preferenceKinds: readonly StaffPreferenceKind[] = [
  "weekdays",
  "time_of_day",
  "location",
  "travel",
  "remote",
  "max_consecutive_days",
  "max_hours_per_week",
];
export const WEIGHTS = [1, 2, 3] as const;
export const timeOfDayParts = ["morning", "day", "evening", "night"] as const;
export const travelExtents = ["none", "regional", "national", "international"] as const;
export const remoteShares = ["never", "some", "mostly", "only"] as const;
export const availabilityKinds: readonly StaffAvailabilityKind[] = ["available", "unavailable", "preferred"];
/** ISO weekdays, Monday first. */
export const ISO_WEEKDAYS = [1, 2, 3, 4, 5, 6, 7] as const;

export const preferenceKindLabel = (t: Translate, kind: string): string => t(`preferenceKind_${kind}`);
/** A weight in words: *nice to have*, *matters*, *important*. */
export const weightLabel = (t: Translate, weight: number): string => t(`weight_${weight}`);
export const availabilityKindLabel = (t: Translate, kind: string): string => t(`availabilityKind_${kind}`);
export const timeOfDayLabel = (t: Translate, part: string): string => t(`timeOfDay_${part}`);
export const travelLabel = (t: Translate, extent: string): string => t(`travel_${extent}`);
export const remoteLabel = (t: Translate, share: string): string => t(`remote_${share}`);

/** A Monday the weekday names are read off (5 January 2026). */
const MONDAY = Date.UTC(2026, 0, 5);

/** An ISO weekday's name in the reader's language, long or short, from a fixed Monday. */
export const weekdayLabel = (
  formatDate: (date: Date, options?: Intl.DateTimeFormatOptions) => string,
  isoWeekday: number,
  width: "long" | "short" = "long",
): string => formatDate(new Date(MONDAY + (isoWeekday - 1) * 86_400_000), { weekday: width, timeZone: "UTC" });

/** Minutes after midnight as `HH:MM`; 1440 is the day's end, `24:00`. */
export const minutesLabel = (minutes: number): string =>
  `${String(Math.floor(minutes / 60)).padStart(2, "0")}:${String(minutes % 60).padStart(2, "0")}`;

/** The quarter hours from `first` to `last` minutes after midnight, both included, as a select's options. */
export const quarterHourOptions = (first = 0, last = 1440) =>
  Array.from({ length: Math.floor((last - first) / 15) + 1 }, (_, index) => {
    const minutes = first + index * 15;
    return { value: String(minutes), label: minutesLabel(minutes) };
  });

/** A declaration starts on a quarter hour from 00:00 to 23:45 … */
export const startOptions = () => quarterHourOptions(0, 1425);
/** … and ends on one from 00:15 to 24:00. */
export const endOptions = () => quarterHourOptions(15, 1440);

/** Whether two declarations say the same thing, field by field (the diff's equality, plan reading 18). */
export const sameDeclaration = (a: StaffAvailabilityDeclaration, b: StaffAvailabilityDeclaration): boolean =>
  a.kind === b.kind &&
  a.weekday === b.weekday &&
  a.fromDate === b.fromDate &&
  a.toDate === b.toDate &&
  a.startMinute === b.startMinute &&
  a.endMinute === b.endMinute;

/** A declaration as the request takes it: its fields and no stamp. */
export const declarationInput = (row: StaffAvailabilityDeclaration): StaffAvailabilityDeclaration => {
  const input = { ...row };
  delete input.seenAt;
  return input;
};

/**
 * A list of declarations as one string, for telling two lists equal
 * whatever their order: each row's fields in a fixed order, no stamp, sorted.
 */
export const declarationsKey = (rows: readonly StaffAvailabilityDeclaration[]): string =>
  JSON.stringify(
    rows
      .map((row) => JSON.stringify([row.kind, row.weekday, row.fromDate, row.toDate, row.startMinute, row.endMinute]))
      .sort(),
  );

/** A list of interests, by their competences' ids, as one string whatever their order. */
export const interestsKey = (ids: readonly string[]): string => [...ids].sort().join(",");

/**
 * Whether an editor's draft follows the server's rows as they change: when
 * it says what the server last said (nothing changed in the editor) or what
 * the server says now (the person's own save came back). Any other draft is
 * kept, and the editor says the rows changed elsewhere.
 */
export const followsServer = (draft: string, seeded: string, loaded: string): boolean =>
  draft === seeded || draft === loaded;

/** The six gap statuses (design S13, plan reading 23), in the order the server judges them. */
export const gapStatuses: readonly StaffCompetenceGapStatus[] = [
  "missing",
  "declared_only",
  "below_level",
  "expired",
  "expiring",
  "met",
];

/** A gap status in words: *Met*, *Declared, not verified*, *Below the level* … */
export const gapStatusLabel = (t: Translate, status: string): string => t(`gapStatus_${status}`);

/** A gap status's badge colour: met teal, expiring orange, declared only yellow, the rest red. */
export const gapStatusColor = (status: StaffCompetenceGapStatus): string => {
  if (status === "met") return "teal";
  if (status === "expiring") return "orange";
  if (status === "declared_only") return "yellow";
  return "red";
};
