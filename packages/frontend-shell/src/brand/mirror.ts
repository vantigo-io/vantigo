import { BRAND_ALGORITHM } from "./scale";
import { BRAND_PAYLOAD_VERSION, type BrandPayload, cssVariables } from "./tokens";

/**
 * The browser's mirror of the brand (brands design B16, B36): `localStorage["vantigo-brand"]`. It is
 * only a fallback, for when the brand stylesheet cannot load (offline, a failed request): the
 * pre-paint script in `apps/server/internal/web/index.go` paints it then, and nothing else. It is
 * versioned `{ v, algorithm }`, keyed by the person it was written for, and cleared on sign-out; a
 * mirror of another person, or of a version this client does not know, is ignored and removed.
 */
export const BRAND_MIRROR_KEY = "vantigo-brand";

/** The variables the pre-paint script applies, per scheme, and the title, for one person. */
export interface BrandMirror {
  v: number;
  algorithm: number;
  userId: string;
  title: string;
  light: Record<string, string>;
  dark: Record<string, string>;
}

/** Whether this client understands a mirror (or a payload) of version `v` and algorithm `algorithm`. */
export const understands = (v: unknown, algorithm: unknown): boolean =>
  typeof v === "number" &&
  v >= 1 &&
  v <= BRAND_PAYLOAD_VERSION &&
  typeof algorithm === "number" &&
  algorithm <= BRAND_ALGORITHM;

export function writeBrandMirror(userId: string, brand: BrandPayload): void {
  try {
    const mirror: BrandMirror = {
      v: BRAND_PAYLOAD_VERSION,
      algorithm: brand.algorithm,
      userId,
      title: brand.title,
      light: cssVariables(brand.tokens, "light"),
      dark: cssVariables(brand.tokens, "dark"),
    };
    window.localStorage.setItem(BRAND_MIRROR_KEY, JSON.stringify(mirror));
  } catch {
    // Local storage is blocked or full: the mirror is only a fallback.
  }
}

/** Removes the mirror (on sign-out, and when it is not the current person's). */
export function clearBrandMirror(): void {
  try {
    window.localStorage.removeItem(BRAND_MIRROR_KEY);
  } catch {
    // Blocked storage has nothing to clear.
  }
}

/** The mirror written for `userId`, or null (and the mirror removed) when it is another person's or unknown. */
export function readBrandMirror(userId: string): BrandMirror | null {
  try {
    const raw = window.localStorage.getItem(BRAND_MIRROR_KEY);
    if (!raw) return null;
    const mirror = JSON.parse(raw) as Partial<BrandMirror> | null;
    if (!mirror || !understands(mirror.v, mirror.algorithm) || mirror.userId !== userId) {
      clearBrandMirror();
      return null;
    }
    return mirror as BrandMirror;
  } catch {
    return null;
  }
}
