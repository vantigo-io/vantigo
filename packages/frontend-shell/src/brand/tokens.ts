/**
 * A brand's generated values (brands design B2, B3, B36), the editor's live preview: a port of the
 * server's `apps/server/internal/identity/brand_tokens.go`, which is the truth. Both are tested
 * against `brand-tokens.golden.json`, and `theme-variables.test.tsx` mounts a MantineProvider with
 * the brand's theme and compares every variable, so a Mantine upgrade that changes a rule fails
 * instead of drifting.
 */
import { BRAND_ALGORITHM, type BrandScale, BrandScaleError, generateScale, WHITE } from "./scale";

export type BrandRadius = "xs" | "sm" | "md" | "lg" | "xl";
export const BRAND_RADII: readonly BrandRadius[] = ["xs", "sm", "md", "lg", "xl"];

/** The logo slots (B1), in the order they are shown. */
export type LogoSlot = "mark_light" | "mark_dark" | "icon" | "wordmark";
export const LOGO_SLOTS: readonly LogoSlot[] = ["mark_light", "mark_dark", "icon", "wordmark"];

/** The value of `--mantine-radius-default` for each radius input. */
export const RADIUS_VALUES: Readonly<Record<BrandRadius, string>> = {
  xs: "calc(0.125rem * var(--mantine-scale))",
  sm: "calc(0.25rem * var(--mantine-scale))",
  md: "calc(0.5rem * var(--mantine-scale))",
  lg: "calc(1rem * var(--mantine-scale))",
  xl: "calc(2rem * var(--mantine-scale))",
};

/**
 * Vantigo's own indigo. Its scale is the hand-tuned one the product shipped with, not the generated
 * one, so an installation that never edits its brand looks exactly as it did; the rule is a function
 * of the input, as on the server.
 */
export const BUILTIN_PRIMARY = "#4f46e5";
export const BUILTIN_SHADES: readonly string[] = [
  "#eef2ff",
  "#e0e7ff",
  "#c7d2fe",
  "#a5b4fc",
  "#818cf8",
  "#6366f1",
  "#4f46e5",
  "#4338ca",
  "#3730a3",
  "#312e81",
];

export interface SchemeTokens {
  shades: readonly string[];
  filledText: string;
  link: string;
  subtleBg: string;
  /** The variables Mantine derives from the shades, as ready CSS variable values. */
  derived: Readonly<Record<string, string>>;
}

export interface BrandTokens {
  light: SchemeTokens;
  dark: SchemeTokens;
  /** The value of `--mantine-radius-default`. */
  radius: string;
}

export interface BrandSupport {
  email?: string;
  phone?: string;
  url?: string;
}

/** The versioned payload of `GET /api/v1/identity/brand` (B36). */
export interface BrandPayload {
  v: number;
  algorithm: number;
  brandId: string;
  version: string;
  name: string;
  title: string;
  tokens: BrandTokens;
  logos: Partial<Record<LogoSlot, string>>;
  support: BrandSupport;
  source: "default" | "preview";
}

/** The payload version this client understands; a higher one is ignored (B36). */
export const BRAND_PAYLOAD_VERSION = 1;

const channels = (hex: string): [number, number, number] => {
  const v = Number.parseInt(hex.slice(1), 16);
  return [(v >> 16) & 255, (v >> 8) & 255, v & 255];
};

/** Mantine's `alpha()`: `rgba(r, g, b, a)`. */
const withAlpha = (hex: string, a: number): string => {
  const [r, g, b] = channels(hex);
  return `rgba(${r}, ${g}, ${b}, ${a})`;
};

/** Mantine's `darken()`: each channel `Math.round(c * (1 - alpha))`. */
const darkenChannel = (c: number, alpha: number): number => Math.round(c * (1 - alpha));

const darkenedRgba = (hex: string, alpha: number): string => {
  const [r, g, b] = channels(hex);
  return `rgba(${darkenChannel(r, alpha)}, ${darkenChannel(g, alpha)}, ${darkenChannel(b, alpha)}, 1)`;
};

const darkenedHex = (hex: string, alpha: number): string => {
  const [r, g, b] = channels(hex);
  const h = (n: number) => darkenChannel(n, alpha).toString(16).padStart(2, "0");
  return `#${h(r)}${h(g)}${h(b)}`;
};

/** The scale and filled-text colour of a primary; throws `BrandScaleError` like `generateScale`. */
export function scaleOf(primary: string): BrandScale {
  if (primary.toLowerCase() === BUILTIN_PRIMARY) {
    return {
      algorithm: BRAND_ALGORITHM,
      scale: BUILTIN_SHADES as unknown as BrandScale["scale"],
      filledText: WHITE,
    };
  }
  return generateScale(primary);
}

const contrastVar = (filledText: string) =>
  filledText === "#000000" ? "var(--mantine-color-black)" : "var(--mantine-color-white)";

/** Everything a primary and a radius generate. Throws `BrandScaleError` for a bad or out-of-range primary. */
export function computeTokens(primary: string, radius: BrandRadius): BrandTokens {
  const res = scaleOf(primary);
  const s = res.scale;
  const light: SchemeTokens = {
    shades: s,
    filledText: res.filledText,
    link: s[8],
    subtleBg: s[0],
    derived: {
      "--mantine-color-vantigo-text": "var(--mantine-color-vantigo-filled)",
      "--mantine-color-vantigo-filled": "var(--mantine-color-vantigo-6)",
      "--mantine-color-vantigo-filled-hover": "var(--mantine-color-vantigo-7)",
      "--mantine-color-vantigo-light": "var(--mantine-color-vantigo-1)",
      "--mantine-color-vantigo-light-hover": "var(--mantine-color-vantigo-2)",
      "--mantine-color-vantigo-light-color": "var(--mantine-color-vantigo-9)",
      "--mantine-color-vantigo-outline": "var(--mantine-color-vantigo-6)",
      "--mantine-color-vantigo-outline-hover": withAlpha(s[6], 0.05),
      "--mantine-primary-color-contrast": contrastVar(res.filledText),
    },
  };
  const dark: SchemeTokens = {
    shades: s,
    filledText: res.filledText,
    link: s[3],
    subtleBg: darkenedHex(s[9], 0.5),
    derived: {
      "--mantine-color-vantigo-text": "var(--mantine-color-vantigo-4)",
      "--mantine-color-vantigo-filled": "var(--mantine-color-vantigo-6)",
      "--mantine-color-vantigo-filled-hover": "var(--mantine-color-vantigo-7)",
      "--mantine-color-vantigo-light": darkenedRgba(s[9], 0.5),
      "--mantine-color-vantigo-light-hover": darkenedRgba(s[9], 0.3),
      "--mantine-color-vantigo-light-color": "var(--mantine-color-vantigo-0)",
      "--mantine-color-vantigo-outline": "var(--mantine-color-vantigo-2)",
      "--mantine-color-vantigo-outline-hover": withAlpha(s[2], 0.05),
      "--mantine-primary-color-contrast": contrastVar(res.filledText),
    },
  };
  return { light, dark, radius: RADIUS_VALUES[radius] };
}

/** `computeTokens`, or null when the primary is not a usable colour (the editor shows it while typing). */
export function tryComputeTokens(primary: string, radius: BrandRadius): BrandTokens | null {
  try {
    return computeTokens(primary, radius);
  } catch (error) {
    if (error instanceof BrandScaleError) return null;
    throw error;
  }
}

/**
 * The CSS variables a scheme sets, as the stylesheet sets them: the ten shades, the radius and the
 * derived set. The mirror and the preview use this.
 */
export function cssVariables(tokens: BrandTokens, scheme: "light" | "dark"): Record<string, string> {
  const out: Record<string, string> = {};
  tokens[scheme].shades.forEach((shade, i) => {
    out[`--mantine-color-vantigo-${i}`] = shade;
  });
  out["--mantine-radius-default"] = tokens.radius;
  return { ...out, ...tokens[scheme].derived };
}

const DEFAULT_RADIUS: BrandRadius = "md";

/** The payload of Vantigo's own default brand, which is what the app shows with nothing from the server. */
export function builtinPayload(): BrandPayload {
  return {
    v: BRAND_PAYLOAD_VERSION,
    algorithm: BRAND_ALGORITHM,
    brandId: "00000000-0000-0000-0000-000000000000",
    version: "builtin",
    name: "Vantigo",
    title: "Vantigo",
    tokens: computeTokens(BUILTIN_PRIMARY, DEFAULT_RADIUS),
    logos: {},
    support: {},
    source: "default",
  };
}
