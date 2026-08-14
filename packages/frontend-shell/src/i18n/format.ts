import { localeToIntlLocale, type SupportedLocale } from "./locale";
import { getLocale } from "./store";

export type DateInput = Date | number | string;
export interface LocaleFormatters {
  formatDate: (value: DateInput, options?: Intl.DateTimeFormatOptions) => string;
  formatNumber: (value: number, options?: Intl.NumberFormatOptions) => string;
  formatCurrency: (
    value: number,
    currency: string,
    options?: Omit<Intl.NumberFormatOptions, "style" | "currency">,
  ) => string;
}

const asDate = (value: DateInput): Date => (value instanceof Date ? value : new Date(value));

export const createLocaleFormatters = (locale: SupportedLocale): LocaleFormatters => {
  const intlLocale = localeToIntlLocale(locale);
  return {
    formatDate: (value, options) =>
      new Intl.DateTimeFormat(intlLocale, options ?? { dateStyle: "medium" }).format(asDate(value)),
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
