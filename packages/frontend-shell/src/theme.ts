import {
  type CSSVariablesResolver,
  createTheme,
  localStorageColorSchemeManager,
  type MantineColorsTuple,
} from "@mantine/core";
import { DARK_TINT_HOVER_PERCENT, DARK_TINT_PERCENT, LUMINANCE_THRESHOLD, overCard } from "./brand/scale";
import { BRAND_RADII, type BrandRadius, type BrandTokens, builtinPayload, RADIUS_VALUES } from "./brand/tokens";

/**
 * The Vantigo brand color scale, anchored on indigo #4f46e5 (shade 6).
 * It is the scale of the default brand (brand/tokens.ts keeps the same ten values, and a test
 * pins the two together); a brand with another primary generates its own.
 */
export const vantigoIndigo: MantineColorsTuple = [
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

/**
 * Mantine's neutral `dark` scale, replaced by a cool slate: a grey with only a
 * little blue in it, so the dark theme keeps the cool feel of the light page
 * (#f7f8fc) while a brand's primary, of any hue, still reads as the one colour
 * on the screen. Mantine reads shade 7 as the surface, 0 as the text, 2 as
 * dimmed text and 4 as borders, so these ten values place every Mantine
 * component. The steps are elevation: 8 is the page, 7 a card, 6 what floats
 * above a card (a menu, a popover) and an input. theme.css pins the page, the
 * surface, the raised surface, the text and the link to the same values;
 * theme.test.ts keeps the two in step.
 */
export const vantigoDark: MantineColorsTuple = [
  "#dfe2ea",
  "#b6bac8",
  "#8b90a3",
  "#686d80",
  "#353947",
  "#2b2e3a",
  "#23262f",
  "#191b24",
  "#0d0f14",
  "#08090d",
];

/**
 * Where the chosen colour scheme is mirrored in local storage: "light", "dark"
 * or "auto". The pre-paint snippet in apps/server/internal/web/index.go reads
 * the same key, so keep the two equal.
 */
export const COLOR_SCHEME_STORAGE_KEY = "vantigo-color-scheme";

/** Pass to the MantineProvider as `colorSchemeManager`, with `defaultColorScheme="auto"`. */
export const vantigoColorSchemeManager = localStorageColorSchemeManager({ key: COLOR_SCHEME_STORAGE_KEY });

/**
 * The semantic colour tokens theme.css defines for both colour schemes. Use
 * these, or a Mantine variable, in styles; never write a colour literal in a
 * component.
 */
export const vantigoTokens = {
  bodyBg: "var(--vantigo-body-bg)",
  surface: "var(--vantigo-surface)",
  text: "var(--vantigo-text)",
  textDimmed: "var(--vantigo-text-dimmed)",
  border: "var(--vantigo-border)",
  link: "var(--vantigo-link)",
  subtleBg: "var(--vantigo-subtle-bg)",
  /** What floats above a surface (a menu, a popover): the surface in light, one step lighter in dark. */
  raised: "var(--vantigo-raised)",
  /** The full-page backdrop of the public pages: Mantine gray-0 in light, the body background in dark. */
  pageBg: "light-dark(var(--mantine-color-gray-0), var(--vantigo-body-bg))",
} as const;

/** The radius input a `--mantine-radius-default` value stands for; `md` for one it does not know. */
const radiusKey = (value: string): BrandRadius => BRAND_RADII.find((r) => RADIUS_VALUES[r] === value) ?? "md";

/**
 * The Mantine theme of a brand (brands design B14): the brand's ten shades as the `vantigo` colour,
 * its radius, and the text colour on a filled primary chosen by Mantine's `autoContrast` at the
 * threshold the server uses to choose it too (0.179, not Mantine's 0.3). With no brand it is the
 * Vantigo default brand exactly. Everything else, the dark neutrals included, is Vantigo's.
 */
export function createVantigoTheme(brand?: { tokens: BrandTokens }) {
  const tokens = brand?.tokens ?? builtinPayload().tokens;
  return createTheme({
    colors: { vantigo: tokens.light.shades as unknown as MantineColorsTuple, dark: vantigoDark },
    primaryColor: "vantigo",
    // Mantine's default dark shade (8) turns indigo muddy on dark grey.
    primaryShade: { light: 6, dark: 6 },
    defaultRadius: radiusKey(tokens.radius),
    autoContrast: true,
    luminanceThreshold: LUMINANCE_THRESHOLD,
  });
}

/**
 * The dark scheme's `light` variant (a tinted background: the active navigation link, an app tile,
 * a light badge) of the brand colour and of grey. Mantine 9 makes it shade 9 darkened by half, which
 * for a dark shade 9 is darker than the card it lies on, so the tint read as a hole. Here it is the
 * colour's shade 5 laid over the card at a quarter strength: always lighter than the card, and the
 * colour the server generates for a brand (brand/tokens.ts, B36). Grey takes the neutral steps.
 * Pass it to the MantineProvider as `cssVariablesResolver`.
 */
export const vantigoCssVariablesResolver: CSSVariablesResolver = (theme) => {
  const shade5 = theme.colors.vantigo?.[5];
  return {
    variables: {},
    light: {},
    dark: {
      ...(shade5
        ? {
            "--mantine-color-vantigo-light": overCard(shade5, DARK_TINT_PERCENT),
            "--mantine-color-vantigo-light-hover": overCard(shade5, DARK_TINT_HOVER_PERCENT),
            "--mantine-color-vantigo-light-color": "var(--mantine-color-vantigo-1)",
          }
        : {}),
      "--mantine-color-gray-light": "var(--mantine-color-dark-5)",
      "--mantine-color-gray-light-hover": "var(--mantine-color-dark-4)",
      "--mantine-color-gray-light-color": "var(--mantine-color-dark-0)",
    },
  };
};

/**
 * The shared Mantine theme for all Vantigo frontend apps, the default brand's. Pass it to the
 * MantineProvider in each app's entry point, alongside importing
 * `@vantigo/frontend-shell/theme.css` for the global styles; the host passes the signed-in
 * person's brand instead (`createVantigoTheme`).
 */
export const vantigoTheme = createVantigoTheme();
