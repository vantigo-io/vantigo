/**
 * Country-aware legal-id validation.
 *
 * Currently only Norway ("NO") has strict rules (mod11 checksums for
 * fødselsnummer and organisasjonsnummer). Other countries get a basic
 * sanity check; country-specific rules can be plugged into the registry
 * as they become relevant.
 */
import type { LegalType } from "@vantigo/customers/database/schema/customers";

function mod11Checksum(digits: number[], weights: number[]): number | null {
  const sum = digits.reduce((acc, digit, i) => acc + digit * weights[i], 0);
  const remainder = sum % 11;
  const check = remainder === 0 ? 0 : 11 - remainder;
  return check === 10 ? null : check;
}

function toDigits(value: string): number[] {
  return [...value].map(Number);
}

/** Norwegian organisasjonsnummer: 9 digits with a mod11 check digit. */
export function validateOrgnr(value: string): boolean {
  if (!/^\d{9}$/.test(value)) return false;
  const digits = toDigits(value);
  const check = mod11Checksum(digits.slice(0, 8), [3, 2, 7, 6, 5, 4, 3, 2]);
  return check !== null && check === digits[8];
}

/** Norwegian fødselsnummer: 11 digits with two mod11 check digits. */
export function validateFodselsnummer(value: string): boolean {
  if (!/^\d{11}$/.test(value)) return false;
  const digits = toDigits(value);

  const k1 = mod11Checksum(digits.slice(0, 9), [3, 7, 6, 1, 8, 9, 4, 5, 2]);
  if (k1 === null || k1 !== digits[9]) return false;

  const k2 = mod11Checksum(digits.slice(0, 10), [5, 4, 3, 2, 7, 6, 5, 4, 3, 2]);
  return k2 !== null && k2 === digits[10];
}

type LegalIdValidator = (legalType: LegalType, legalId: string) => boolean;

const countryRules: Record<string, LegalIdValidator> = {
  NO: (legalType, legalId) =>
    legalType === "business" ? validateOrgnr(legalId) : validateFodselsnummer(legalId),
};

/**
 * Validates a legal id for the given country and legal type.
 * Countries without registered rules only require a non-empty value
 * of reasonable length.
 */
export function validateLegalId(country: string, legalType: LegalType, legalId: string): boolean {
  const rule = countryRules[country.toUpperCase()];
  if (rule) return rule(legalType, legalId);
  return legalId.trim().length >= 4 && legalId.trim().length <= 32;
}
