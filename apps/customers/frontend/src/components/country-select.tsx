import { Select, type SelectProps } from "@mantine/core";
import type { SupportedLocale } from "@vantigo/frontend-shell";
import { countrySelectData } from "../lib/country-display";

export interface CountrySelectProps extends Omit<SelectProps, "data" | "searchable"> {
  locale: SupportedLocale;
}

/**
 * A searchable country picker over the full ISO 3166-1 alpha-2 list
 * (controller ruling, design D3) — codes are sent lower-case, the same
 * convention the legal identity's own country field already uses.
 */
export const CountrySelect = ({ locale, ...props }: CountrySelectProps) => (
  <Select searchable data={countrySelectData(locale)} {...props} />
);
