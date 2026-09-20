import { useI18n, useLocale } from "@vantigo/frontend-shell";
import "../i18n";
import { formatCalendarDate } from "./dates";

/**
 * What this reader writes between a whole number and its decimals. Every
 * amount input in the package takes it, so somebody who types `1,50` in
 * Norwegian is not told that it is not a number — the value on the wire is a
 * number either way.
 */
export const useDecimalSeparator = (): string => new Intl.NumberFormat(useLocale()).format(1.1).charAt(1);

/**
 * The one way this package writes money, dates and distances. Money is always
 * written in the expense's own currency — nothing in Expenses is ever
 * converted — and a calendar date is formatted in UTC, so a day never slides
 * by one for a reader west of Greenwich.
 */
export const useExpenseFormat = () => {
  const { t, formatters } = useI18n("expenses");
  return {
    t,
    /**
     * An amount in the expense's own currency — nothing here is ever
     * converted. Before `/meta` has answered there is no currency to write it
     * in, so the bare number is written rather than a guessed one.
     */
    money: (amount: number, currency: string) =>
      currency
        ? formatters.formatCurrency(amount, currency)
        : formatters.formatNumber(amount, { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
    /** An amount with no currency behind it is written as a bare number, never guessed at. */
    number: (value: number, decimals = 2) =>
      formatters.formatNumber(value, { minimumFractionDigits: decimals, maximumFractionDigits: decimals }),
    date: (date: string) => formatCalendarDate(date, formatters.formatDate),
    dateTime: (instant: string) => formatters.formatDate(new Date(instant), { dateStyle: "medium" }),
    distance: (km: number) => t("kilometresShort", { km: formatters.formatNumber(km, { maximumFractionDigits: 1 }) }),
  };
};
