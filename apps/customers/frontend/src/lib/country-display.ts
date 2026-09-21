import type { SupportedLocale } from "@vantigo/frontend-shell";
import { iso3166Alpha2Codes } from "./country-codes";

/** Norway, Sweden, Denmark, Finland — pinned first (controller ruling). */
const PINNED_CODES = ["no", "se", "dk", "fi"];

/**
 * The customers module has no dedicated `SupportedLocale` → BCP-47 mapping of
 * its own; `@vantigo/frontend-shell` keeps one (`localeToIntlLocale`) but
 * does not export it from the package root, so this is a narrow copy of the
 * same two-locale rule for `Intl.DisplayNames`.
 */
const intlLocale = (locale: SupportedLocale) => (locale === "nb" ? "nb-NO" : "en-US");

/** A country code's localized display name, falling back to the bare code if `Intl` cannot name it. */
export const countryDisplayName = (code: string, locale: SupportedLocale): string => {
  try {
    return new Intl.DisplayNames([intlLocale(locale)], { type: "region" }).of(code.toUpperCase()) ?? code.toUpperCase();
  } catch {
    return code.toUpperCase();
  }
};

/**
 * The `<Select>` `data` for every officially assigned ISO 3166-1 alpha-2
 * country (controller ruling), localized names first, Norway/Sweden/Denmark/
 * Finland pinned at the top, the rest alphabetical by that localized name.
 */
export const countrySelectData = (locale: SupportedLocale) => {
  const named = (code: string) => ({ value: code, label: countryDisplayName(code, locale) });
  const pinned = PINNED_CODES.map(named);
  const rest = iso3166Alpha2Codes
    .filter((code) => !PINNED_CODES.includes(code))
    .map(named)
    .sort((a, b) => a.label.localeCompare(b.label, intlLocale(locale)));
  return [...pinned, ...rest];
};
