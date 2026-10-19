import { localeToIntlLocale, type SupportedLocale } from "./locale";
import { getLocale } from "./store";
import { getTimeZones } from "./time-zone";

export type DateInput = Date | number | string;
export interface LocaleFormatters {
  /**
   * An instant in the viewer's time zone (time zones design D8). A `timeZone`
   * the options name wins: the date-only convention passes `"UTC"` with a
   * `T00:00:00Z` value, so a civil date never shifts.
   */
  formatDate: (value: DateInput, options?: Intl.DateTimeFormatOptions) => string;
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string;
  formatCurrency: (
    value: number,
    currency: string,
    options?: Omit<Intl.NumberFormatOptions, "style" | "currency">,
  ) => string;
}

const asDate = (value: DateInput): Date => (value instanceof Date ? value : new Date(value));

/**
 * The formatters for a locale, formatting instants in `timeZone`: by default
 * the viewer's effective zone as the host last set it (`setTimeZones`), the
 * browser's own until then.
 */
export const createLocaleFormatters = (
  locale: SupportedLocale,
  timeZone: string = getTimeZones().timeZone,
): LocaleFormatters => {
  const intlLocale = localeToIntlLocale(locale);
  return {
    formatDate: (value, options) => {
      const resolved = options ?? { dateStyle: "medium" };
      return new Intl.DateTimeFormat(intlLocale, { ...resolved, timeZone: resolved.timeZone ?? timeZone }).format(
        asDate(value),
      );
    },
    formatNumber: (value, options) => new Intl.NumberFormat(intlLocale, options).format(value),
    formatCurrency: (value, currency, options) =>
      new Intl.NumberFormat(intlLocale, { ...options, style: "currency", currency }).format(value),
  };
};

export const formatDate = (value: DateInput, options?: Intl.DateTimeFormatOptions) =>
  createLocaleFormatters(getLocale()).formatDate(value, options);
export const formatNumber = (value: number, options?: Intl.NumberFormatOptions) =>
  createLocaleFormatters(getLocale()).formatNumber(value, options);
export const formatCurrency = (
  value: number,
  currency: string,
  options?: Omit<Intl.NumberFormatOptions, "style" | "currency">,
) => createLocaleFormatters(getLocale()).formatCurrency(value, currency, options);
