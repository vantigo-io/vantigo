import { useI18n } from "@vantigo/frontend-shell";
import "../i18n";

/** A calendar date read in UTC, so a day never slides by one. */
const toUtc = (date: string): Date => new Date(`${date}T00:00:00.000Z`);

/**
 * The one way this package writes money, numbers and dates. Every amount is
 * the server's decimal text (P17); nothing here computes money.
 */
export const usePurchasingFormat = () => {
  const { t, formatters } = useI18n("purchasing");
  return {
    t,
    money: (amount: string | number, currency: string) => formatters.formatCurrency(Number(amount), currency),
    /** A unit price, which takes up to four decimals (P11). */
    unitPrice: (amount: string | number, currency: string) =>
      formatters.formatCurrency(Number(amount), currency, { minimumFractionDigits: 2, maximumFractionDigits: 4 }),
    quantity: (value: string | number) => formatters.formatNumber(Number(value), { maximumFractionDigits: 3 }),
    date: (date: string) => formatters.formatDate(toUtc(date), { dateStyle: "medium", timeZone: "UTC" }),
    dateTime: (instant: string) =>
      formatters.formatDate(new Date(instant), { dateStyle: "medium", timeStyle: "short" }),
  };
};

/** Today as the calendar names it, in the reader's zone. */
export const today = (): string => {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
};

/** A day `days` after a calendar date. */
export const addDays = (date: string, days: number): string => {
  const next = toUtc(date);
  next.setUTCDate(next.getUTCDate() + days);
  return next.toISOString().slice(0, 10);
};

/** A fresh idempotency key for a send (A11). */
export const newRequestId = (): string =>
  typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (c) =>
        (Number(c) ^ (Math.floor(Math.random() * 16) >> (Number(c) / 4))).toString(16),
      );
