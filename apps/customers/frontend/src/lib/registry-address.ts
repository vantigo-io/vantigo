import type { CustomerAddressType } from "../api/addresses";
import type { CustomerRegistryAddress } from "../api/registry";

/**
 * What a registry address looks like once it is an add-address form's starting
 * point (design D3). Deliberately only the fields the registry can answer for:
 * `label`, `region` and `isPrimary` are the user's to decide, and the form's
 * own defaults already say so.
 */
export interface RegistryAddressValues {
  type: CustomerAddressType;
  line1: string;
  line2: string;
  postalCode: string;
  city: string;
  country: string;
}

/**
 * Turns one of the registry's addresses into the values the add-address modal
 * opens with (design D3): the registry keeps an address as a free-form array
 * of lines and the form has exactly two, so the first line is line 1 and
 * whatever follows is joined into line 2 rather than dropped. `countryCode` is
 * ISO 3166-1 alpha-2 upper-case on the wire and lower-case in this API, and
 * the form's own default stands in when the registry named no country at all.
 */
export const registryAddressValues = (
  address: CustomerRegistryAddress,
  type: CustomerAddressType,
): RegistryAddressValues => ({
  type,
  line1: address.lines[0] ?? "",
  line2: address.lines.slice(1).join(", "),
  postalCode: address.postalCode ?? "",
  city: address.city ?? "",
  country: address.countryCode ? address.countryCode.toLocaleLowerCase() : "no",
});
