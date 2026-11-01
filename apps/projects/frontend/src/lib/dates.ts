import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/**
 * A project's plain calendar dates. They are formatted in UTC so a local
 * evening never moves a date a day, and an absent end (or start) shows the
 * catalog's dash rather than an empty half-range.
 */
export const useProjectDates = () => {
  const { t, formatters } = useI18n("projects");
  const day = (value: string) => formatters.formatDate(value, { dateStyle: "medium", timeZone: "UTC" });
  return {
    day,
    /** "5 Jan 2026 – 31 Mar 2026", with the dash alone when neither date is set. */
    range: (start?: string | null, end?: string | null) => {
      const from = start ? day(start) : null;
      const to = end ? day(end) : null;
      return from || to ? `${from ?? t("notAvailable")} – ${to ?? t("notAvailable")}` : t("notAvailable");
    },
  };
};

/** A shift of whole days in words: "2 days later", "1 day earlier". */
export const useDayShiftLabel = () => {
  const { t } = useI18n("projects");
  return (days: number) => (days >= 0 ? t("shiftDaysLater", { count: days }) : t("shiftDaysEarlier", { count: -days }));
};

/**
 * The calendar date `date` falls on where the browser is, as YYYY-MM-DD.
 * Not `toISOString()`, which is the UTC date: just after midnight in Oslo
 * that is still yesterday.
 */
export const localIsoDate = (date: Date = new Date()): string => {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
};
