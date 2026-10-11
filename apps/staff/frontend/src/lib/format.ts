import { useI18n, useLocale } from "@vantigo/frontend-shell";
import "../i18n";
import { formatCalendarDate } from "./dates";

/**
 * What this reader writes between a whole number and its decimals, so a
 * percentage typed as `80,5` in Norwegian is not refused as not a number.
 */
export const useDecimalSeparator = (): string => new Intl.NumberFormat(useLocale()).format(1.1).charAt(1);

/** The one way this package writes a calendar date and a number. */
export const useStaffFormat = () => {
  const { t, formatters } = useI18n("staff");
  return {
    t,
    date: (date: string) => formatCalendarDate(date, formatters.formatDate),
    number: (value: number) => formatters.formatNumber(value, { maximumFractionDigits: 2 }),
  };
};
