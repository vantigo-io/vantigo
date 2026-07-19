import type { Customer } from "@vantigo/customers/database/schema/customers";

export const preferredCustomerTypes = ["business", "private", "last"] as const;

export type PreferredCustomerType = (typeof preferredCustomerTypes)[number];

export function isPreferredCustomerType(value: unknown): value is PreferredCustomerType {
  return typeof value === "string" && (preferredCustomerTypes as readonly string[]).includes(value);
}

export const defaultPreferredCustomerType: PreferredCustomerType = "business";

/** Resolves the default legal type for new customers from the user's preferences. */
export function resolveDefaultLegalType(
  preferredCustomerType: string | null | undefined,
  lastCustomerType: string | null | undefined,
): Customer["legalType"] {
  if (preferredCustomerType === "private") return "private";
  if (preferredCustomerType === "last") {
    return lastCustomerType === "private" ? "private" : "business";
  }
  return "business";
}
