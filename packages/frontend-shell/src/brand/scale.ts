/**
 * A brand's 10-shade colour scale and its contrast checks (brands design B3 and B4).
 *
 * This is the editor's live preview: a port of the server's
 * `apps/server/internal/brandscale`, which is the truth (it validates and stores).
 * The two are tested against one golden file, `brand-scale.golden.json`, so they
 * cannot drift. Keep the arithmetic in the same order as the Go code; changing
 * any constant is a new `BRAND_ALGORITHM`.
 */

/** The version of the constants below, stored with every published token set. */
export const BRAND_ALGORITHM = 1;

export const MIN_LIGHTNESS = 0.35;
export const MAX_LIGHTNESS = 0.85;
/** AA for text. */
export const MIN_CONTRAST = 4.5;
/** Mantine's autoContrast threshold, set explicitly in the theme (its default is 0.3). */
export const LUMINANCE_THRESHOLD = 0.179;

export const WHITE = "#ffffff";
export const BLACK = "#000000";

/** The token layer's page, card and text colours (`theme.css`); a test keeps them equal. */
export const PAGE_LIGHT = "#f7f8fc";
export const CARD_LIGHT = "#ffffff";
export const TEXT_LIGHT = "#182033";
export const PAGE_DARK = "#15182a";
export const CARD_DARK = "#1c1f32";
export const TEXT_DARK = "#dde1ef";

export type BrandScaleErrorCode = "brand_hex" | "brand_primary_range";

/** A primary that is not `#rrggbb` (`brand_hex`) or whose lightness is out of range (`brand_primary_range`). */
export class BrandScaleError extends Error {
  readonly code: BrandScaleErrorCode;
  constructor(code: BrandScaleErrorCode) {
    super(code === "brand_hex" ? "the colour is not #rrggbb" : "the primary's lightness is outside 0.35 to 0.85");
    this.name = "BrandScaleError";
    this.code = code;
  }
}

export type Scheme = "light" | "dark";
export type Pair = "filled" | "filledHover" | "linkPage" | "linkCard" | "linkTint";

/** The order the checks come in, within each scheme. */
export const PAIRS: readonly Pair[] = ["filled", "filledHover", "linkPage", "linkCard", "linkTint"];

/** Shades 0 (lightest) to 9 (darkest). */
export type Shades = readonly [string, string, string, string, string, string, string, string, string, string];

export interface BrandScale {
  algorithm: number;
  /** Shade 6 is the input. */
  scale: Shades;
  /** The text colour on a filled primary: white or black. */
  filledText: string;
}

export interface ContrastCheck {
  pair: Pair;
  scheme: Scheme;
  foreground: string;
  background: string;
  ratio: number;
  pass: boolean;
}

type Matrix = readonly [
  readonly [number, number, number],
  readonly [number, number, number],
  readonly [number, number, number],
];
interface Lab {
  l: number;
  a: number;
  b: number;
}

const LIGHT_STEPS = [1.0, 0.86, 0.68, 0.5, 0.32, 0.16] as const;
const DARK_STEPS = [0.33, 0.66, 1.0] as const;
const CHROMA_K = [0.12, 0.25, 0.45, 0.68, 0.88, 1.0, 1.0, 0.95, 0.85, 0.72] as const;
const LIGHT_END = 0.975;
const DARK_END = 0.28;
const GAMUT_TOLERANCE = 1e-6;
const GAMUT_STEPS = 24;
const SUGGEST_STEP = 0.005;
const SUGGEST_MAX = 200;

// Björn Ottosson's two published matrices, and their inverses.
const LINEAR_TO_LMS: Matrix = [
  [0.4122214708, 0.5363325363, 0.0514459929],
  [0.2119034982, 0.6806995451, 0.1073969566],
  [0.0883024619, 0.2817188376, 0.6299787005],
];
const LMS_TO_LAB: Matrix = [
  [0.2104542553, 0.793617785, -0.0040720468],
  [1.9779984951, -2.428592205, 0.4505937099],
  [0.0259040371, 0.7827717662, -0.808675766],
];
const LAB_TO_LMS: Matrix = [
  [1, 0.3963377774, 0.2158037573],
  [1, -0.1055613458, -0.0638541728],
  [1, -0.0894841775, -1.291485548],
];
const LMS_TO_LINEAR: Matrix = [
  [4.0767416621, -3.3077115913, 0.2309699292],
  [-1.2684380046, 2.6097574011, -0.3413193965],
  [-0.0041960863, -0.7034186147, 1.707614701],
];

const HEX = /^#[0-9a-fA-F]{6}$/;

function dot(m: readonly [number, number, number], x: number, y: number, z: number): number {
  return m[0] * x + m[1] * y + m[2] * z;
}

function parseHex(hex: string): [number, number, number] {
  if (!HEX.test(hex)) {
    throw new BrandScaleError("brand_hex");
  }
  return [
    Number.parseInt(hex.slice(1, 3), 16),
    Number.parseInt(hex.slice(3, 5), 16),
    Number.parseInt(hex.slice(5, 7), 16),
  ];
}

function formatHex(r: number, g: number, b: number): string {
  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

/** An sRGB channel in [0, 1] as linear light (IEC 61966-2-1). */
function decode(c: number): number {
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function encode(c: number): number {
  return c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055;
}

function toOKLab(r: number, g: number, b: number): Lab {
  const lr = decode(r / 255);
  const lg = decode(g / 255);
  const lb = decode(b / 255);
  const l = Math.cbrt(dot(LINEAR_TO_LMS[0], lr, lg, lb));
  const m = Math.cbrt(dot(LINEAR_TO_LMS[1], lr, lg, lb));
  const s = Math.cbrt(dot(LINEAR_TO_LMS[2], lr, lg, lb));
  return { l: dot(LMS_TO_LAB[0], l, m, s), a: dot(LMS_TO_LAB[1], l, m, s), b: dot(LMS_TO_LAB[2], l, m, s) };
}

function linear(c: Lab): [number, number, number] {
  const l = dot(LAB_TO_LMS[0], c.l, c.a, c.b);
  const m = dot(LAB_TO_LMS[1], c.l, c.a, c.b);
  const s = dot(LAB_TO_LMS[2], c.l, c.a, c.b);
  const l3 = l * l * l;
  const m3 = m * m * m;
  const s3 = s * s * s;
  return [dot(LMS_TO_LINEAR[0], l3, m3, s3), dot(LMS_TO_LINEAR[1], l3, m3, s3), dot(LMS_TO_LINEAR[2], l3, m3, s3)];
}

function inGamut(c: Lab): boolean {
  return linear(c).every((v) => v >= -GAMUT_TOLERANCE && v <= 1 + GAMUT_TOLERANCE);
}

/** c with its chroma scaled into sRGB: 24 bisections of a chroma scale in [0, 1], the lower bound kept. */
function fit(c: Lab): Lab {
  if (inGamut(c)) {
    return c;
  }
  let lo = 0;
  let hi = 1;
  for (let i = 0; i < GAMUT_STEPS; i++) {
    const mid = (lo + hi) / 2;
    if (inGamut({ l: c.l, a: c.a * mid, b: c.b * mid })) {
      lo = mid;
    } else {
      hi = mid;
    }
  }
  return { l: c.l, a: c.a * lo, b: c.b * lo };
}

function channel(v: number): number {
  return Math.floor(encode(Math.min(Math.max(v, 0), 1)) * 255 + 0.5);
}

function hexOf(c: Lab): string {
  const [r, g, b] = linear(c);
  return formatHex(channel(r), channel(g), channel(b));
}

/** The OKLCH lightness of a `#rrggbb` colour. */
export function lightness(hex: string): number {
  const [r, g, b] = parseHex(hex);
  return toOKLab(r, g, b).l;
}

/** The scale of a primary. Throws a `BrandScaleError` for a bad colour or one out of the lightness range. */
export function generateScale(hex: string): BrandScale {
  const [r, g, b] = parseHex(hex);
  const base = toOKLab(r, g, b);
  if (base.l < MIN_LIGHTNESS || base.l > MAX_LIGHTNESS) {
    throw new BrandScaleError("brand_primary_range");
  }
  const scale: string[] = [];
  for (let i = 0; i < 10; i++) {
    let l: number;
    if (i < 6) {
      l = base.l + (LIGHT_END - base.l) * (LIGHT_STEPS[i] as number);
    } else if (i === 6) {
      l = base.l;
    } else {
      l = base.l - (base.l - DARK_END) * (DARK_STEPS[i - 7] as number);
    }
    const k = CHROMA_K[i] as number;
    scale.push(hexOf(fit({ l, a: base.a * k, b: base.b * k })));
  }
  scale[6] = formatHex(r, g, b);
  return { algorithm: BRAND_ALGORITHM, scale: scale as unknown as Shades, filledText: filledText(scale[6] as string) };
}

/** WCAG 2.x relative luminance. */
function luminance(hex: string): number {
  const [r, g, b] = parseHex(hex);
  return 0.2126 * decode(r / 255) + 0.7152 * decode(g / 255) + 0.0722 * decode(b / 255);
}

/** The text colour on a filled background as Mantine's autoContrast chooses it: white up to the threshold, black above. */
export function filledText(background: string): string {
  return luminance(background) > LUMINANCE_THRESHOLD ? BLACK : WHITE;
}

/** The WCAG 2.x contrast ratio of two colours, from 1 to 21. */
export function contrast(a: string, b: string): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

/**
 * Mantine's `light` variant of a colour in the dark scheme: shade 9 darkened by half, each
 * channel halved and rounded half-up. It is opaque, so compositing it over the card changes nothing.
 */
export function darkTint(shade9: string): string {
  const [r, g, b] = parseHex(shade9);
  return formatHex(Math.floor((r + 1) / 2), Math.floor((g + 1) / 2), Math.floor((b + 1) / 2));
}

/** The ten checks of B4 for a generated scale, light then dark, each in the order of `PAIRS` (AA, 4.5 : 1). */
export function contrastChecks(result: BrandScale): ContrastCheck[] {
  const s = result.scale;
  const text = result.filledText;
  const rows: [Scheme, Pair, string, string][] = [
    ["light", "filled", text, s[6]],
    ["light", "filledHover", text, s[7]],
    ["light", "linkPage", s[8], PAGE_LIGHT],
    ["light", "linkCard", s[8], CARD_LIGHT],
    ["light", "linkTint", s[8], s[0]],
    ["dark", "filled", text, s[6]],
    ["dark", "filledHover", text, s[7]],
    ["dark", "linkPage", s[3], PAGE_DARK],
    ["dark", "linkCard", s[3], CARD_DARK],
    ["dark", "linkTint", s[3], darkTint(s[9])],
  ];
  return rows.map(([scheme, pair, foreground, background]) => {
    const ratio = contrast(foreground, background);
    return { pair, scheme, foreground, background, ratio, pass: ratio >= MIN_CONTRAST };
  });
}

/** Whether every check passes. */
export function passes(checks: readonly ContrastCheck[]): boolean {
  return checks.every((c) => c.pass);
}

/**
 * The nearest primary that passes every check: the input's hue and chroma with its lightness moved,
 * 0.005 at a time, darker before lighter, inside the accepted range, the chroma fitted into sRGB.
 * It is the input itself when that already passes, and null when none does.
 */
export function suggestPrimary(hex: string): string | null {
  let result: BrandScale;
  try {
    result = generateScale(hex);
  } catch {
    return null;
  }
  if (passes(contrastChecks(result))) {
    return result.scale[6];
  }
  const [r, g, b] = parseHex(hex);
  const base = toOKLab(r, g, b);
  for (let n = 1; n <= SUGGEST_MAX; n++) {
    for (const sign of [-1, 1]) {
      const l = base.l + sign * (SUGGEST_STEP * n);
      if (l < MIN_LIGHTNESS || l > MAX_LIGHTNESS) {
        continue;
      }
      const candidate = hexOf(fit({ l, a: base.a, b: base.b }));
      try {
        if (passes(contrastChecks(generateScale(candidate)))) {
          return candidate;
        }
      } catch {
        // Out of the accepted range after rounding: not a candidate.
      }
    }
  }
  return null;
}
