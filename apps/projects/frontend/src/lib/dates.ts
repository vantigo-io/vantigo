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
