/**
 * Country presentation helpers: emoji flags and localized country names.
 * Pure functions — the ISO alpha-2 code remains the stored/transferred value.
 */

const REGIONAL_INDICATOR_OFFSET = 0x1f1e6 - 0x41; // 'A' → 🇦

/**
 * Returns the emoji flag for an ISO 3166-1 alpha-2 code ("NO" → 🇳🇴).
 * Note: Windows/Chrome renders these as letter pairs — acceptable fallback.
 */
export function countryFlag(code: string): string {
  const normalized = code.trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(normalized)) return "";
  return String.fromCodePoint(
    ...[...normalized].map((char) => char.charCodeAt(0) + REGIONAL_INDICATOR_OFFSET),
  );
}

/** Localized country name via Intl.DisplayNames; falls back to the code. */
export function countryName(code: string, locale: string): string {
  const normalized = code.trim().toUpperCase();
  if (!/^[A-Z]{2}$/.test(normalized)) return code;
  try {
    return new Intl.DisplayNames([locale], { type: "region" }).of(normalized) ?? normalized;
  } catch {
    return normalized;
  }
}

/** "🇳🇴 Norway" — flag-prefixed localized label for selects and lists. */
export function countryLabel(code: string, locale: string): string {
  const flag = countryFlag(code);
  const name = countryName(code, locale);
  return flag ? `${flag} ${name}` : name;
}
